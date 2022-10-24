/*
Copyright 2022 The Kubernetes Authors.

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

package cel

import (
	"encoding/json"
	"fmt"

	celtypes "github.com/google/cel-go/common/types"
	"github.com/google/cel-go/interpreter"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/api/admissionregistration/v1alpha1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/admission/plugin/cel/matching"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

var _ ValidatorCompiler = &CELValidatorCompiler{}
var _ matching.MatchCriteria = &matchCriteria{}

type matchCriteria struct {
	constraints *v1alpha1.MatchResources
}

// GetParsedNamespaceSelector is to get the converted LabelSelector which implements labels.Selector
func (m *matchCriteria) GetParsedNamespaceSelector() (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(m.constraints.NamespaceSelector)
}

// GetParsedObjectSelector is to get the converted LabelSelector which implements labels.Selector
func (m *matchCriteria) GetParsedObjectSelector() (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(m.constraints.ObjectSelector)
}

// GetMatchResources is to get the matchConstraints
func (m *matchCriteria) GetMatchResources() v1alpha1.MatchResources {
	return *m.constraints
}

// CELValidatorCompiler implement the interface ValidatorCompiler.
type CELValidatorCompiler struct {
	Matcher *matching.Matcher
}

// SetExternalKubeInformerFactory registers the namespaceLister into Matcher.
func (c *CELValidatorCompiler) SetExternalKubeInformerFactory(factory informers.SharedInformerFactory) {
	c.Matcher.SetNamespaceLister(factory.Core().V1().Namespaces().Lister())
}

// SetExternalKubeClientSet registers client into Matcher
func (c *CELValidatorCompiler) SetExternalKubeClientSet(client kubernetes.Interface) {
	c.Matcher.SetExternalKubeClientSet(client)
}

// DefinitionMatches returns whether this ValidatingAdmissionPolicy matches the provided admission resource request
func (c *CELValidatorCompiler) DefinitionMatches(definition *v1alpha1.ValidatingAdmissionPolicy, a admission.Attributes, o admission.ObjectInterfaces) (bool, error) {
	criteria := matchCriteria{constraints: definition.Spec.MatchConstraints}
	return c.Matcher.Matches(&criteria, a, o)
}

// BindingMatches returns whether this ValidatingAdmissionPolicyBinding matches the provided admission resource request
func (c *CELValidatorCompiler) BindingMatches(binding *v1alpha1.ValidatingAdmissionPolicyBinding, a admission.Attributes, o admission.ObjectInterfaces) (bool, error) {
	if binding.Spec.MatchResources == nil {
		return true, nil
	}
	criteria := matchCriteria{constraints: binding.Spec.MatchResources}
	return c.Matcher.Matches(&criteria, a, o)
}

// ValidateInitialization checks if Matcher is initialized.
func (c *CELValidatorCompiler) ValidateInitialization() error {
	return c.Matcher.ValidateInitialization()
}

type validationActivation struct {
	object    *unstructured.Unstructured
	oldObject *unstructured.Unstructured
	params    *unstructured.Unstructured
	request   *unstructured.Unstructured
	hasOld    bool
}

// ResolveName returns a value from the activation by qualified name, or false if the name
// could not be found.
func (a *validationActivation) ResolveName(name string) (interface{}, bool) {
	switch name {
	case ObjectVarName:
		return a.object.Object, true
	case OldObjectVarName:
		return a.oldObject.Object, a.hasOld
	case ParamsVarName:
		return a.params.Object, true
	case RequestVarName:
		return a.request.Object, true
	default:
		return nil, false
	}
}

// Parent returns the parent of the current activation, may be nil.
// If non-nil, the parent will be searched during resolve calls.
func (a *validationActivation) Parent() interpreter.Activation {
	return nil
}

// Compile compiles the cel expression defined in ValidatingAdmissionPolicy
func (c *CELValidatorCompiler) Compile(p *v1alpha1.ValidatingAdmissionPolicy) (Validator, error) {
	if len(p.Spec.Validations) == 0 {
		return nil, nil
	}
	hasParam := false
	if p.Spec.ParamKind != nil {
		hasParam = true
	}
	compilationResults := make([]CompilationResult, len(p.Spec.Validations))
	for i, validation := range p.Spec.Validations {
		compilationResults[i] = CompileValidatingPolicyExpression(validation.Expression, hasParam)
	}
	return &CELValidator{policy: p, compilationResults: compilationResults}, nil
}

// CELValidator implements the Validator interface
type CELValidator struct {
	policy             *v1alpha1.ValidatingAdmissionPolicy
	compilationResults []CompilationResult
}

func convertObjectToUnstructured(obj interface{}) *unstructured.Unstructured {
	var unstructuredObj unstructured.Unstructured
	jsonObj, _ := json.Marshal(obj)
	_ = json.Unmarshal(jsonObj, &unstructuredObj.Object)

	return &unstructuredObj
}

// Validate validates all cel expressions in Validator and returns PolicyDecisions and error.
func (v *CELValidator) Validate(a admission.Attributes, o admission.ObjectInterfaces, params *unstructured.Unstructured) ([]PolicyDecision, error) {
	decisions := make([]PolicyDecision, len(v.policy.Spec.Validations))
	var oldObjectVal *unstructured.Unstructured
	if a.GetOldObject() != nil {
		oldObjectVal = convertObjectToUnstructured(a.GetOldObject())
	}

	var objectVal *unstructured.Unstructured
	if a.GetObject() != nil {
		objectVal = convertObjectToUnstructured(a.GetObject())
	}
	va := &validationActivation{
		object:    objectVal,
		oldObject: oldObjectVal,
		params:    params,
		request:   convertObjectToUnstructured(createAdmissionRequest(a)),
	}

	for i, compilationResult := range v.compilationResults {
		var policyDecision = &decisions[i]

		if compilationResult.Error != nil {
			policyDecision.Kind = Deny
			policyDecision.Message = fmt.Sprintf("compilation error: %v", compilationResult.Error)
			continue
		}
		if compilationResult.Program == nil {
			continue
		}
		evalResult, _, err := compilationResult.Program.Eval(va)
		if err != nil {
			f := *v.policy.Spec.FailurePolicy
			if f == v1alpha1.Ignore {
				policyDecision.Kind = Admit
			} else {
				policyDecision.Kind = Deny
			}
			policyDecision.Message = fmt.Sprintf("failed expression: %v, with evaluation error: %v", v.policy.Spec.Validations[i].Expression, err)
		} else if evalResult != celtypes.True {
			policyDecision.Kind = Deny
			reason := v.policy.Spec.Validations[i].Reason
			if reason == nil {
				policyDecision.Reason = metav1.StatusReasonInvalid
			} else {
				policyDecision.Reason = *reason
			}
			policyDecision.Message = fmt.Sprintf("failed expression: %v", v.policy.Spec.Validations[i].Expression)
		} else {
			policyDecision.Kind = Admit
		}
	}

	return decisions, nil
}

func createAdmissionRequest(attr admission.Attributes) *admissionv1.AdmissionRequest {
	gvk := attr.GetKind()
	gvr := attr.GetResource()
	subresource := attr.GetSubresource()

	// FIXME: how to get resource GVK, GVR and subresource?
	requestGVK := attr.GetKind()
	requestGVR := attr.GetResource()
	requestSubResource := attr.GetSubresource()

	aUserInfo := attr.GetUserInfo()
	var userInfo authenticationv1.UserInfo
	if aUserInfo != nil {
		userInfo = authenticationv1.UserInfo{
			Extra:    make(map[string]authenticationv1.ExtraValue),
			Groups:   aUserInfo.GetGroups(),
			UID:      aUserInfo.GetUID(),
			Username: aUserInfo.GetName(),
		}
		// Convert the extra information in the user object
		for key, val := range aUserInfo.GetExtra() {
			userInfo.Extra[key] = authenticationv1.ExtraValue(val)
		}
	}

	dryRun := attr.IsDryRun()

	return &admissionv1.AdmissionRequest{
		Kind: metav1.GroupVersionKind{
			Group:   gvk.Group,
			Kind:    gvk.Kind,
			Version: gvk.Version,
		},
		Resource: metav1.GroupVersionResource{
			Group:    gvr.Group,
			Resource: gvr.Resource,
			Version:  gvr.Version,
		},
		SubResource: subresource,
		RequestKind: &metav1.GroupVersionKind{
			Group:   requestGVK.Group,
			Kind:    requestGVK.Kind,
			Version: requestGVK.Version,
		},
		RequestResource: &metav1.GroupVersionResource{
			Group:    requestGVR.Group,
			Resource: requestGVR.Resource,
			Version:  requestGVR.Version,
		},
		RequestSubResource: requestSubResource,
		Name:               attr.GetName(),
		Namespace:          attr.GetNamespace(),
		Operation:          admissionv1.Operation(attr.GetOperation()),
		UserInfo:           userInfo,
		// Leave Object and OldObject unset since we don't provide access to them via request
		DryRun: &dryRun,
		Options: runtime.RawExtension{
			Object: attr.GetOperationOptions(),
		},
	}
}
