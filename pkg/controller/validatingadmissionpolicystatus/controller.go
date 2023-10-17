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
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/api/admissionregistration/v1beta1"
	extscheme "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/admission/plugin/validatingadmissionpolicy"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
	admissionregistrationv1beta1apply "k8s.io/client-go/applyconfigurations/admissionregistration/v1beta1"
	informerv1beta1 "k8s.io/client-go/informers/admissionregistration/v1beta1"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	admissionregistrationv1beta1 "k8s.io/client-go/kubernetes/typed/admissionregistration/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kubernetes/pkg/controller/validatingadmissionpolicystatus/schemawatcher"
	generated "k8s.io/kubernetes/pkg/generated/openapi"
)

// ControllerName has "Status" in it to differentiate this controller with the other that runs in API server.
const ControllerName = "validatingadmissionpolicy-status"

// Controller is the ValidatingAdmissionPolicy Status controller that reconciles the Status field of each policy object.
// This controller runs type checks against referred types for each policy definition.
type Controller struct {
	policyInformer informerv1beta1.ValidatingAdmissionPolicyInformer
	policyQueue    workqueue.RateLimitingInterface
	policySynced   cache.InformerSynced
	policyClient   admissionregistrationv1beta1.ValidatingAdmissionPolicyInterface

	// typeChecker checks the policy's expressions for type errors.
	// Type of params is defined in policy.Spec.ParamsKind
	// Types of object are calculated from policy.Spec.MatchingConstraints
	typeChecker *validatingadmissionpolicy.TypeChecker

	// typesMu protects referenceTracker and schemaWatcher, and the interaction of both.
	typesMu          sync.Mutex
	referenceTracker *referenceTracker
	schemaWatcher    *schemawatcher.OpenAPIv3Discovery
}

func (c *Controller) Run(ctx context.Context, workers int) {
	defer utilruntime.HandleCrash()

	if !cache.WaitForNamedCacheSync(ControllerName, ctx.Done(), c.policySynced) {
		return
	}

	defer c.policyQueue.ShutDown()
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	go c.schemaWatcher.Run(ctx)
	go c.watchForSchemaChanges(ctx)

	<-ctx.Done()
}

func NewController(
	policyInformer informerv1beta1.ValidatingAdmissionPolicyInformer,
	policyClient admissionregistrationv1beta1.ValidatingAdmissionPolicyInterface,
	restMapper meta.RESTMapper,
	schemaWatcher *schemawatcher.OpenAPIv3Discovery,
) (*Controller, error) {
	typeChecker := &validatingadmissionpolicy.TypeChecker{
		SchemaResolver: newNativeFirstSchemaResolver(schemaWatcher),
		RestMapper:     restMapper,
	}
	c := &Controller{
		policyInformer:   policyInformer,
		policyQueue:      workqueue.NewRateLimitingQueueWithConfig(workqueue.DefaultControllerRateLimiter(), workqueue.RateLimitingQueueConfig{Name: ControllerName}),
		policyClient:     policyClient,
		typeChecker:      typeChecker,
		referenceTracker: newReferenceTracker(),
		schemaWatcher:    schemaWatcher,
	}
	reg, err := policyInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.enqueuePolicy(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			c.enqueuePolicy(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			c.enqueuePolicy(obj)
		},
	})
	if err != nil {
		return nil, err
	}
	c.policySynced = reg.HasSynced
	return c, nil
}

func (c *Controller) enqueuePolicy(object any) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(object)
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.policyQueue.Add(key)
}

func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.policyQueue.Get()
	if shutdown {
		return false
	}
	defer c.policyQueue.Done(key)

	err := c.syncPolicy(ctx, key)
	if err == nil {
		c.policyQueue.Forget(key)
		return true
	}

	utilruntime.HandleError(err)
	c.policyQueue.AddRateLimited(key)

	return true
}

func (c *Controller) syncPolicy(ctx context.Context, key any) error {
	metaNamespaceKey, ok := key.(string)
	if !ok {
		return fmt.Errorf("expect a string but got %v", key)
	}
	_, name, err := cache.SplitMetaNamespaceKey(metaNamespaceKey)
	if err != nil {
		return err
	}
	policy, err := c.policyInformer.Lister().Get(name)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// handle deletion
			c.updateTypeTracking(ctx, name, nil)
			return nil
		}
		return err
	}
	return c.reconcile(ctx, policy)
}

func (c *Controller) reconcile(ctx context.Context, policy *v1beta1.ValidatingAdmissionPolicy) error {
	if policy == nil {
		return nil
	}
	if policy.Generation < policy.Status.ObservedGeneration {
		return nil
	}
	typeCheckingCtx := c.typeChecker.CreateContext(policy)
	c.updateTypeTracking(ctx, policy.Name, typeCheckingCtx.GVKs())
	warnings := c.typeChecker.CheckWithContext(typeCheckingCtx, policy)
	warningsConfig := make([]*admissionregistrationv1beta1apply.ExpressionWarningApplyConfiguration, 0, len(warnings))
	for _, warning := range warnings {
		warningsConfig = append(warningsConfig, admissionregistrationv1beta1apply.ExpressionWarning().
			WithFieldRef(warning.FieldRef).
			WithWarning(warning.Warning))
	}
	applyConfig := admissionregistrationv1beta1apply.ValidatingAdmissionPolicy(policy.Name).
		WithStatus(admissionregistrationv1beta1apply.ValidatingAdmissionPolicyStatus().
			WithObservedGeneration(policy.Generation).
			WithTypeChecking(admissionregistrationv1beta1apply.TypeChecking().
				WithExpressionWarnings(warningsConfig...)))
	_, err := c.policyClient.ApplyStatus(ctx, applyConfig, metav1.ApplyOptions{FieldManager: ControllerName, Force: true})
	return err
}

func (c *Controller) watchForSchemaChanges(ctx context.Context) {
	for {
		select {
		case gvk := <-c.schemaWatcher.ChangedGVKsChan:
			c.typesMu.Lock()
			affactedPolicyNames := c.referenceTracker.affectedPolicies(gvk)
			c.typesMu.Unlock()
			for _, name := range affactedPolicyNames {
				c.enqueuePolicy(cache.ObjectName{Name: name}.String())
			}
		case <-ctx.Done():
			return
		}
	}
}
func (c *Controller) updateTypeTracking(ctx context.Context, policyName string, gvks []schema.GroupVersionKind) {
	extensionTypes := filterExtensionTypes(gvks)
	c.typesMu.Lock()
	defer c.typesMu.Unlock()
	sub, unsub := c.referenceTracker.observePolicyUpdate(policyName, extensionTypes)
	if len(sub) > 0 {
		c.schemaWatcher.Subscribe(ctx, sub...)
	}
	if len(unsub) > 0 {
		c.schemaWatcher.Unsubscribe(ctx, unsub...)
	}
}

// isExtensionType checks if a GVK is an extension type.
// An extensions type is defined as a type that has a group that are not known by any compiled-in schemes.
// Currently, schemes of both Kubernetes client and extensions client are considered.
func isExtensionType(gvk schema.GroupVersionKind) bool {
	return gvk.Group != "" && // empty group for core
		!k8sscheme.Scheme.IsGroupRegistered(gvk.Group) &&
		!extscheme.Scheme.IsGroupRegistered(gvk.Group)
}

// filterExtensionTypes extracts all GVKs that are extensions types.
func filterExtensionTypes(gvks []schema.GroupVersionKind) []schema.GroupVersionKind {
	result := make([]schema.GroupVersionKind, 0, len(gvks))
	for _, gvk := range gvks {
		if isExtensionType(gvk) {
			result = append(result, gvk)
		}
	}
	return result
}

type nativeFirstSchemaResolver struct {
	definitionsSchemaResolver *resolver.DefinitionsSchemaResolver
	extensionResolver         resolver.SchemaResolver
}

func newNativeFirstSchemaResolver(extensionResolver resolver.SchemaResolver) *nativeFirstSchemaResolver {
	return &nativeFirstSchemaResolver{
		definitionsSchemaResolver: resolver.NewDefinitionsSchemaResolver(generated.GetOpenAPIDefinitions,
			k8sscheme.Scheme, extscheme.Scheme),
		extensionResolver: extensionResolver,
	}
}
func (r *nativeFirstSchemaResolver) ResolveSchema(gvk schema.GroupVersionKind) (*spec.Schema, error) {
	if isExtensionType(gvk) {
		return r.extensionResolver.ResolveSchema(gvk)
	}
	return r.definitionsSchemaResolver.ResolveSchema(gvk)
}
