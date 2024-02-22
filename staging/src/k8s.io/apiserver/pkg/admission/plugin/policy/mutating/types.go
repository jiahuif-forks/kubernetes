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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Temporary API types to use for unit testing until the real types are available.
type MutatingAdmissionPolicy struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   MutatingAdmissionPolicySpec
	Status MutatingAdmissionPolicyStatus
}

type MutatingAdmissionPolicyBinding struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec MutatingAdmissionPolicyBindingSpec
}

type MutatingAdmissionPolicySpec struct {
	ParamKind          *v1beta1.ParamKind              `json:"paramKind,omitempty" protobuf:"bytes,1,rep,name=paramKind"`
	MatchConstraints   *v1beta1.MatchResources         `json:"matchConstraints,omitempty" protobuf:"bytes,2,rep,name=matchConstraints"`
	Mutations          []Mutation                      `json:"validations,omitempty" protobuf:"bytes,3,rep,name=validations"`
	AuditAnnotations   []v1beta1.AuditAnnotation       `json:"auditAnnotations,omitempty" protobuf:"bytes,5,rep,name=auditAnnotations"`
	MatchConditions    []v1beta1.MatchCondition        `json:"matchConditions,omitempty" patchStrategy:"merge" patchMergeKey:"name" protobuf:"bytes,6,rep,name=matchConditions"`
	Variables          []v1beta1.Variable              `json:"variables,omitempty" patchStrategy:"merge" patchMergeKey:"name" protobuf:"bytes,7,rep,name=variables"`
	ReinvocationPolicy *v1beta1.ReinvocationPolicyType `json:"reinvocationPolicy,omitempty" protobuf:"bytes,10,opt,name=reinvocationPolicy,casttype=ReinvocationPolicyType"`
}

type Mutation struct {
	PatchType  string `json:"patchType" protobuf:"bytes,1,opt,name=patchType"`
	Expression string `json:"expression" protobuf:"bytes,1,opt,name=expression"`
}

type MutatingAdmissionPolicyBindingSpec struct {
	PolicyName     string                  `json:"policyName,omitempty" protobuf:"bytes,1,rep,name=policyName"`
	ParamRef       *v1beta1.ParamRef       `json:"paramRef,omitempty" protobuf:"bytes,2,rep,name=paramRef"`
	MatchResources *v1beta1.MatchResources `json:"matchResources,omitempty" protobuf:"bytes,3,rep,name=matchResources"`
}

type MutatingAdmissionPolicyStatus struct {
	ObservedGeneration int64                 `json:"observedGeneration,omitempty" protobuf:"varint,1,opt,name=observedGeneration"`
	TypeChecking       *v1beta1.TypeChecking `json:"typeChecking,omitempty" protobuf:"bytes,2,opt,name=typeChecking"`
	Conditions         []metav1.Condition    `json:"conditions,omitempty" protobuf:"bytes,3,rep,name=conditions"`
}

func (p *MutatingAdmissionPolicy) DeepCopyObject() runtime.Object {
	// Shallow copy, since we are lazy and don't want to implement a deep copy.
	return &MutatingAdmissionPolicy{
		TypeMeta:   p.TypeMeta,
		ObjectMeta: *p.ObjectMeta.DeepCopy(),
		Spec:       p.Spec,
		Status:     p.Status,
	}
}

func (p *MutatingAdmissionPolicy) GetObjectKind() schema.ObjectKind {
	return p
}

func (p *MutatingAdmissionPolicyBinding) DeepCopyObject() runtime.Object {
	// Shallow copy, since we are lazy and don't want to implement a deep copy.
	return &MutatingAdmissionPolicyBinding{
		TypeMeta:   p.TypeMeta,
		ObjectMeta: *p.ObjectMeta.DeepCopy(),
		Spec:       p.Spec,
	}
}

func (p *MutatingAdmissionPolicyBinding) GetObjectKind() schema.ObjectKind {
	return p
}
