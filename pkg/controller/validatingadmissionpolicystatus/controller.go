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
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1client "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	informers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/admission/plugin/validatingadmissionpolicy"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
	admissionregistrationv1alpha1apply "k8s.io/client-go/applyconfigurations/admissionregistration/v1alpha1"
	informerv1alpha1 "k8s.io/client-go/informers/admissionregistration/v1alpha1"
	"k8s.io/client-go/kubernetes/scheme"
	admissionregistrationv1alpha1 "k8s.io/client-go/kubernetes/typed/admissionregistration/v1alpha1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	generated "k8s.io/kubernetes/pkg/generated/openapi"
)

// ControllerName has "Status" in it to differentiate this controller with the other that runs in API server.
const ControllerName = "validatingadmissionpolicy-status"

// Controller is the ValidatingAdmissionPolicy Status controller that reconciles the Status field of each policy object.
// This controller runs type checks against referred types for each policy definition.
//
// There is no CRD queue. Instead, a change of CRD causes all policy definitions affected by the CRD change
// to be enqueued to policyQueue.
type Controller struct {
	policyInformer informerv1alpha1.ValidatingAdmissionPolicyInformer
	policyQueue    workqueue.RateLimitingInterface
	policySynced   cache.InformerSynced
	policyClient   admissionregistrationv1alpha1.ValidatingAdmissionPolicyInterface

	crdInformer informers.CustomResourceDefinitionInformer
	crdSynced   cache.InformerSynced
	crdClient   apiextensionsv1client.CustomResourceDefinitionInterface

	crdTracker *crdTracker

	restMapper               meta.RESTMapper
	compiledInSchemaResolver *resolver.DefinitionsSchemaResolver
}

func (c *Controller) Run(ctx context.Context, workers int) {
	defer utilruntime.HandleCrash()

	if !cache.WaitForNamedCacheSync(ControllerName, ctx.Done(), c.policySynced) {
		return
	}

	if !cache.WaitForNamedCacheSync(ControllerName, ctx.Done(), c.crdSynced) {
		return
	}

	defer c.policyQueue.ShutDown()
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	<-ctx.Done()
}

func NewController(policyInformer informerv1alpha1.ValidatingAdmissionPolicyInformer, policyClient admissionregistrationv1alpha1.ValidatingAdmissionPolicyInterface, crdInformer informers.CustomResourceDefinitionInformer, crdClient apiextensionsv1client.CustomResourceDefinitionInterface, restMapper meta.RESTMapper) (*Controller, error) {
	c := &Controller{
		policyInformer:           policyInformer,
		policyQueue:              workqueue.NewRateLimitingQueueWithConfig(workqueue.DefaultControllerRateLimiter(), workqueue.RateLimitingQueueConfig{Name: ControllerName}),
		policyClient:             policyClient,
		crdInformer:              crdInformer,
		crdClient:                crdClient,
		restMapper:               restMapper,
		compiledInSchemaResolver: resolver.NewDefinitionsSchemaResolver(scheme.Scheme, generated.GetOpenAPIDefinitions),
	}
	c.crdTracker = newCRDTracker(c)
	reg, err := policyInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				c.policyQueue.Add(key)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(newObj)
			if err == nil {
				c.policyQueue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				c.policyQueue.Add(key)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	c.policySynced = reg.HasSynced

	reg, err = crdInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition); ok {
				c.crdTracker.handleCRDChange(crd)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// Names of CRDs are immutable, so there can only be schema change.
			// Handle schema change as if it is a new CRD.
			if crd, ok := newObj.(*apiextensionsv1.CustomResourceDefinition); ok {
				c.crdTracker.handleCRDChange(crd)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	c.crdSynced = reg.HasSynced

	return c, nil
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
	err := c.reconcile(ctx, key)
	if err == nil {
		c.policyQueue.Forget(key)
		return true
	}

	utilruntime.HandleError(err)
	c.policyQueue.AddRateLimited(key)

	return true
}

func (c *Controller) reconcile(ctx context.Context, key any) error {
	policyName, ok := key.(string)
	if !ok {
		return fmt.Errorf("expect a string but got %v", key)
	}
	policy, err := c.policyInformer.Lister().Get(policyName)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// Handle deletion, unregister CRD dependencies
			c.crdTracker.updateDependenciesForPolicyDeletion(policyName)
			return nil
		}
		return err
	}
	// policy generation must be strictly less than the status to skip, because of potential requeue due to CRD change.
	if policy.Generation < policy.Status.ObservedGeneration {
		return nil
	}
	cachedCRDResolver := newCachedTypeResolver(c.restMapper, c.compiledInSchemaResolver, c.crdInformer.Lister())
	// cachedCRDResolver locally resolves GVKs and schemas and act as both the SchemaResolver and RestMapper
	typeChecker := &validatingadmissionpolicy.TypeChecker{
		SchemaResolver: cachedCRDResolver,
		RestMapper:     cachedCRDResolver,
	}
	warnings := typeChecker.Check(policy)
	// keep a record of referred CRDs
	c.crdTracker.updateDependenciesForPolicy(policy.Name, cachedCRDResolver.ReferredGroupResources())

	warningsConfig := make([]*admissionregistrationv1alpha1apply.ExpressionWarningApplyConfiguration, 0, len(warnings))
	for _, warning := range warnings {
		warningsConfig = append(warningsConfig, admissionregistrationv1alpha1apply.ExpressionWarning().
			WithFieldRef(warning.FieldRef).
			WithWarning(warning.Warning))
	}
	applyConfig := admissionregistrationv1alpha1apply.ValidatingAdmissionPolicy(policy.Name).
		WithStatus(admissionregistrationv1alpha1apply.ValidatingAdmissionPolicyStatus().
			WithObservedGeneration(policy.Generation).
			WithTypeChecking(admissionregistrationv1alpha1apply.TypeChecking().
				WithExpressionWarnings(warningsConfig...)))
	_, err = c.policyClient.ApplyStatus(ctx, applyConfig, metav1.ApplyOptions{FieldManager: ControllerName, Force: true})
	return err
}
