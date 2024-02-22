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
	"k8s.io/api/admissionregistration/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/admission/plugin/policy/generic"
)

func NewMutatingAdmissionPolicyAccessor(obj *Policy) generic.PolicyAccessor {
	return &mutatingAdmissionPolicyAccessor{
		Policy: obj,
	}
}

func NewMutatingAdmissionPolicyBindingAccessor(obj *PolicyBinding) generic.BindingAccessor {
	return &mutatingAdmissionPolicyBindingAccessor{
		PolicyBinding: obj,
	}
}

type mutatingAdmissionPolicyAccessor struct {
	*Policy
}

func (v *mutatingAdmissionPolicyAccessor) GetNamespace() string {
	return v.Namespace
}

func (v *mutatingAdmissionPolicyAccessor) GetName() string {
	return v.Name
}

func (v *mutatingAdmissionPolicyAccessor) GetParamKind() *v1beta1.ParamKind {
	return v.Spec.ParamKind
}

func (v *mutatingAdmissionPolicyAccessor) GetMatchConstraints() *v1beta1.MatchResources {
	return v.Spec.MatchConstraints
}

type mutatingAdmissionPolicyBindingAccessor struct {
	*PolicyBinding
}

func (v *mutatingAdmissionPolicyBindingAccessor) GetNamespace() string {
	return v.Namespace
}

func (v *mutatingAdmissionPolicyBindingAccessor) GetName() string {
	return v.Name
}

func (v *mutatingAdmissionPolicyBindingAccessor) GetPolicyName() types.NamespacedName {
	return types.NamespacedName{
		Namespace: "",
		Name:      v.Spec.PolicyName,
	}
}

func (v *mutatingAdmissionPolicyBindingAccessor) GetMatchResources() *v1beta1.MatchResources {
	return v.Spec.MatchResources
}

func (v *mutatingAdmissionPolicyBindingAccessor) GetParamRef() *v1beta1.ParamRef {
	return v.Spec.ParamRef
}
