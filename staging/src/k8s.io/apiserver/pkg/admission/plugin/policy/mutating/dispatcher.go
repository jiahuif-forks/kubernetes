/*
Copyright 2024 The Kubernetes Authors.

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

package mutating

import (
	"context"
	"fmt"

	"k8s.io/api/admissionregistration/v1beta1"
	v1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/admission/plugin/policy/generic"
	"k8s.io/apiserver/pkg/admission/plugin/policy/matching"
	"k8s.io/apiserver/pkg/admission/plugin/policy/mutating/patch"
	webhookgeneric "k8s.io/apiserver/pkg/admission/plugin/webhook/generic"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/apiserver/pkg/authorization/authorizer"
)

func NewDispatcher(a authorizer.Authorizer, m *matching.Matcher, tcm patch.TypeConverterManager) generic.Dispatcher[PolicyHook] {
	res := &dispatcher{
		matcher: m,
		//!TODO: pass in static type converter to reduce network calls
		typeConverterManager: tcm,
	}
	res.Dispatcher = generic.NewPolicyDispatcher[*Policy, *PolicyBinding, PolicyEvaluator](
		NewMutatingAdmissionPolicyAccessor,
		NewMutatingAdmissionPolicyBindingAccessor,
		m,
		res.dispatchInvocations,
	)
	return res
}

type dispatcher struct {
	matcher              *matching.Matcher
	typeConverterManager patch.TypeConverterManager
	generic.Dispatcher[PolicyHook]
}

func (d *dispatcher) Run(ctx context.Context) error {
	go d.typeConverterManager.Run(ctx)
	return d.Dispatcher.Run(ctx)
}

func (d *dispatcher) Dispatch(ctx context.Context, a admission.Attributes, o admission.ObjectInterfaces, hooks []PolicyHook) error {
	return d.Dispatcher.Dispatch(ctx, a, o, hooks)
}

func (d *dispatcher) dispatchInvocations(
	ctx context.Context,
	a admission.Attributes,
	o admission.ObjectInterfaces,
	versionedAttributes webhookgeneric.VersionedAttributeAccessor,
	invocations []generic.PolicyInvocation[*Policy, *PolicyBinding, PolicyEvaluator],
) error {
	var lastVersionedAttr *admission.VersionedAttributes

	reinvokeCtx := a.GetReinvocationContext()
	var policyReinvokeCtx *policyReinvokeContext
	if v := reinvokeCtx.Value(PluginName); v != nil {
		policyReinvokeCtx = v.(*policyReinvokeContext)
	} else {
		policyReinvokeCtx = &policyReinvokeContext{}
		reinvokeCtx.SetValue(PluginName, policyReinvokeCtx)
	}

	if reinvokeCtx.IsReinvoke() && policyReinvokeCtx.IsOutputChangedSinceLastPolicyInvocation(a.GetObject()) {
		// If the object has changed, we know the in-tree plugin re-invocations have mutated the object,
		// and we need to reinvoke all eligible policies.
		policyReinvokeCtx.RequireReinvokingPreviouslyInvokedPlugins()
	}
	defer func() {
		policyReinvokeCtx.SetLastPolicyInvocationOutput(a.GetObject())
	}()

	// Should loop through invocations, handling possible error and invoking
	// evaluator to apply patch, also should handle re-invocations

	for _, invocation := range invocations {
		invocationKey, err := keyFor(invocation)
		if err != nil {
			// This should never happen. It occurs if there is a programming
			// error causing the Param not to be a valid object.
			utilruntime.HandleError(err)
			return k8serrors.NewInternalError(err)
		}

		if reinvokeCtx.IsReinvoke() && !policyReinvokeCtx.ShouldReinvoke(invocationKey) {
			continue
		}

		versionedAttr, err := versionedAttributes.VersionedAttribute(invocation.Kind)
		if err != nil {
			return k8serrors.NewInternalError(err)
		}
		lastVersionedAttr = versionedAttr

		changed, err := d.dispatchOne(ctx, a, o, versionedAttr, invocation)
		if err != nil {
			switch err.(type) {
			case *k8serrors.StatusError:
				return err
			default:
				return k8serrors.NewInternalError(err)
			}
		}

		if changed {
			// Patch had changed the object. Prepare to reinvoke all previous webhooks that are eligible for re-invocation.
			policyReinvokeCtx.RequireReinvokingPreviouslyInvokedPlugins()
			reinvokeCtx.SetShouldReinvoke()
		}
		if invocation.Policy.Spec.ReinvocationPolicy != nil && *invocation.Policy.Spec.ReinvocationPolicy == v1beta1.IfNeededReinvocationPolicy {
			policyReinvokeCtx.AddReinvocablePolicyToPreviouslyInvoked(invocationKey)
		}
	}

	if lastVersionedAttr != nil && lastVersionedAttr.VersionedObject != nil && lastVersionedAttr.Dirty {
		return o.GetObjectConvertor().Convert(lastVersionedAttr.VersionedObject, lastVersionedAttr.Attributes.GetObject(), nil)
	}

	return nil
}

func (d *dispatcher) dispatchOne(
	ctx context.Context,
	a admission.Attributes,
	o admission.ObjectInterfaces,
	versionedAttributes *admission.VersionedAttributes,
	invocation generic.PolicyInvocation[*Policy, *PolicyBinding, PolicyEvaluator],
) (changed bool, err error) {
	var namespace *v1.Namespace
	namespaceName := a.GetNamespace()

	// Special case, the namespace object has the namespace of itself (maybe a bug).
	// unset it if the incoming object is a namespace
	if gvk := a.GetKind(); gvk.Kind == "Namespace" && gvk.Version == "v1" && gvk.Group == "" {
		namespaceName = ""
	}

	// if it is cluster scoped, namespaceName will be empty
	// Otherwise, get the Namespace resource.
	if namespaceName != "" {
		namespace, err = d.matcher.GetNamespace(namespaceName)
		if err != nil {
			return false, k8serrors.NewInternalError(fmt.Errorf("failed to get namespace %s: %v", namespaceName, err))
		}
	}

	if invocation.Evaluator == nil {
		// internal error
		return false, k8serrors.NewInternalError(fmt.Errorf("policy evaluator is nil"))
	}

	// Find type converter for the invoked Group-Version.
	typeConverter := d.typeConverterManager.GetTypeConverter(versionedAttributes.VersionedKind)
	if typeConverter == nil {
		return false, k8serrors.NewServiceUnavailable(fmt.Sprintf("failed to get type converter for %s", versionedAttributes.VersionedKind))
	}

	patch, err := invocation.Evaluator(
		ctx,
		invocation.Resource,
		versionedAttributes,
		o,
		invocation.Param,
		namespace,
		typeConverter,
		celconfig.RuntimeCELCostBudget,
	)
	if err != nil {
		return false, err
	}

	newVersionedObject := patch.GetPatchedObject()
	changed = !apiequality.Semantic.DeepEqual(versionedAttributes.VersionedObject, newVersionedObject)
	versionedAttributes.Dirty = true
	versionedAttributes.VersionedObject = newVersionedObject
	o.GetObjectDefaulter().Default(newVersionedObject)
	return changed, nil
}

func keyFor(invocation generic.PolicyInvocation[*Policy, *PolicyBinding, PolicyEvaluator]) (key, error) {
	var paramUID types.UID
	if invocation.Param != nil {
		paramAccessor, err := meta.Accessor(invocation.Param)
		if err != nil {
			// This should never happen, as the param should have been validated
			// before being passed to the plugin.
			return key{}, err
		}
		paramUID = paramAccessor.GetUID()
	}

	return key{
		PolicyUID:  invocation.Policy.GetUID(),
		BindingUID: invocation.Binding.GetUID(),
		ParamUID:   paramUID,
	}, nil
}
