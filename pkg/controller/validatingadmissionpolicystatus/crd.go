/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package validatingadmissionpolicystatus

import (
	"encoding/json"
	"sync"

	"k8s.io/apiextensions-apiserver/pkg/apihelpers"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionslisterv1 "k8s.io/apiextensions-apiserver/pkg/client/listers/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

type crdTracker struct {
	// affectedPolicies is the (CRD GroupResources) -> (affected policy names) mapping
	affectedPolicies map[schema.GroupResource]sets.Set[string]
	// policyDependencies is the (policy name) -> (required CRD GroupResource) mapping.
	// It is the reverse of affectedPolicies.
	policyDependencies map[string]sets.Set[schema.GroupResource]

	// mu protects affectedPolicies and policyDependencies
	mu sync.RWMutex

	parent *Controller
}

// convertCRDValidationToSchema converts the openAPIV3Schema to a kube-openapi schema.
// In case of nil validation or missing OpenAPIV3Schema, this function returns ErrSchemaNotFound.
// The CustomResourceValidation object must be valid. Anything from the informer or client should have already passed
// validation.
// This function uses a JSON marshal-unmarshal conversion instead of Structural because
//   - OpenAPIV3Schema is already validated and thus always a valid OpenAPI schema;
//   - Structural involves private api-extensions API types. We have import-boss to prevent anything outside
//     apiextensions-apiserver package to use them.
func convertCRDValidationToSchema(validation *apiextensionsv1.CustomResourceValidation) (*spec.Schema, error) {
	if validation == nil || validation.OpenAPIV3Schema == nil {
		return nil, resolver.ErrSchemaNotFound
	}
	b, err := json.Marshal(validation.OpenAPIV3Schema)
	if err != nil {
		return nil, err
	}
	s := new(spec.Schema)
	err = json.Unmarshal(b, s)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func newCRDTracker(controller *Controller) *crdTracker {
	return &crdTracker{
		affectedPolicies:   make(map[schema.GroupResource]sets.Set[string]),
		policyDependencies: make(map[string]sets.Set[schema.GroupResource]),
		parent:             controller,
	}
}

// handleCRDChange handles incoming changes to CRDs.
// CRDs are not enqueued. Instead, policy definitions that are affected by the changed CRDs are enqueued to policyQueue.
func (t *crdTracker) handleCRDChange(crd *apiextensionsv1.CustomResourceDefinition) {
	if !apihelpers.IsCRDConditionTrue(crd, apiextensionsv1.Established) {
		return
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	gr := schema.GroupResource{
		Group:    crd.Spec.Group,
		Resource: crd.Spec.Names.Plural,
	}
	if set, ok := t.affectedPolicies[gr]; ok {
		for name := range set {
			t.parent.policyQueue.Add(name)
		}
	}
}

// updateDependenciesForPolicy registers the mapping of the policy.
func (t *crdTracker) updateDependenciesForPolicy(policyName string, grs []schema.GroupResource) {
	if len(grs) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	observedSet := sets.New(grs...)
	oldSet, ok := t.policyDependencies[policyName]
	if ok {
		if oldSet.Equal(observedSet) {
			return
		}
		// remove the ones that are no longer required by the policy
		for gr := range oldSet {
			if !observedSet.Has(gr) {
				if set, ok := t.affectedPolicies[gr]; ok {
					set.Delete(policyName)
					if set.Len() == 0 {
						delete(t.affectedPolicies, gr)
					}
				}
			}
		}
	}
	t.policyDependencies[policyName] = observedSet
	for _, gr := range grs {
		set, ok := t.affectedPolicies[gr]
		if !ok {
			set = sets.New[string]()
			t.affectedPolicies[gr] = set
		}
		set.Insert(policyName)
	}
}

// updateDependenciesForPolicyDeletion unregisters the mapping of the policy being deleted.
func (t *crdTracker) updateDependenciesForPolicyDeletion(policyName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var dependencies []schema.GroupResource
	if set, ok := t.policyDependencies[policyName]; ok {
		dependencies = set.UnsortedList()
	} else {
		return
	}
	delete(t.policyDependencies, policyName)
	for _, gvk := range dependencies {
		if set, ok := t.affectedPolicies[gvk]; ok {
			set.Delete(policyName)
			if set.Len() == 0 {
				delete(t.affectedPolicies, gvk)
			}
		}
	}
}

func CRDVersionToGVK(crd *apiextensionsv1.CustomResourceDefinition, crdVersion *apiextensionsv1.CustomResourceDefinitionVersion) schema.GroupVersionKind {
	gvk := schema.GroupVersionKind{
		Group:   crd.Spec.Group,
		Version: crdVersion.Name,
		Kind:    crd.Spec.Names.Kind,
	}
	return gvk
}

type cachedTypeResolver struct {
	meta.RESTMapper
	resolver.SchemaResolver
	crdLister apiextensionslisterv1.CustomResourceDefinitionLister

	cachedCRD    map[schema.GroupResource]*apiextensionsv1.CustomResourceDefinition
	cachedSchema map[schema.GroupVersionKind]*spec.Schema
}

func newCachedTypeResolver(restMapper meta.RESTMapper, resolver resolver.SchemaResolver, crdLister apiextensionslisterv1.CustomResourceDefinitionLister) *cachedTypeResolver {
	return &cachedTypeResolver{
		RESTMapper:     restMapper,
		SchemaResolver: resolver,
		crdLister:      crdLister,
		cachedCRD:      map[schema.GroupResource]*apiextensionsv1.CustomResourceDefinition{},
		cachedSchema:   map[schema.GroupVersionKind]*spec.Schema{},
	}
}

func (r *cachedTypeResolver) ResolveSchema(gvk schema.GroupVersionKind) (*spec.Schema, error) {
	if s, ok := r.cachedSchema[gvk]; ok {
		return s, nil
	}
	return r.SchemaResolver.ResolveSchema(gvk)
}

func (r *cachedTypeResolver) KindsFor(resource schema.GroupVersionResource) ([]schema.GroupVersionKind, error) {
	if isGroupBuiltin(resource.Group) {
		return r.RESTMapper.KindsFor(resource)
	}

	crd, ok := r.cachedCRD[resource.GroupResource()]
	if !ok {
		var err error
		// Assuming resource.Resource is plural.
		// There does not seem to be a good way to reliably to convert the name without the CRD itself
		crdName := resource.Resource + "." + resource.Group
		crd, err = r.crdLister.Get(crdName)
		if err != nil {
			if kerrors.IsNotFound(err) {
				// also keep negative cache
				r.cachedCRD[resource.GroupResource()] = nil
				return nil, nil
			}
			return nil, err
		}
		r.cachedCRD[resource.GroupResource()] = crd

		for _, v := range crd.Spec.Versions {
			gvk := CRDVersionToGVK(crd, &v)
			s, err := convertCRDValidationToSchema(v.Schema)
			if err != nil {
				return nil, err
			}
			r.cachedSchema[gvk] = s
		}
	}

	var gvks []schema.GroupVersionKind
	for _, v := range crd.Spec.Versions {
		gvk := CRDVersionToGVK(crd, &v)
		if v.Name == gvk.Version {
			gvks = append(gvks, gvk)
		}
	}
	return gvks, nil
}

func (r *cachedTypeResolver) ReferredGroupResources() []schema.GroupResource {
	grs := make([]schema.GroupResource, 0, len(r.cachedCRD))
	for gr := range r.cachedCRD {
		grs = append(grs, gr)
	}
	return grs
}

// isGroupBuiltin checks whether a GVR/GVK is a built-in type by looking at its Group.
func isGroupBuiltin(groupName string) bool {
	return scheme.Scheme.IsGroupRegistered(groupName)
}

var _ meta.RESTMapper = (*cachedTypeResolver)(nil)
var _ resolver.SchemaResolver = (*cachedTypeResolver)(nil)
