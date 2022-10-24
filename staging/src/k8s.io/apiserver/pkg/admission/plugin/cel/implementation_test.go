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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/api/admissionregistration/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
)

func TestCompile(t *testing.T) {
	cases := []struct {
		name             string
		policy           *v1alpha1.ValidatingAdmissionPolicy
		errorExpressions map[string]string
	}{
		{
			name: "invalid syntax",
			policy: &v1alpha1.ValidatingAdmissionPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
				},
				Spec: v1alpha1.ValidatingAdmissionPolicySpec{
					FailurePolicy: func() *v1alpha1.FailurePolicyType {
						r := v1alpha1.FailurePolicyType("Fail")
						return &r
					}(),
					ParamKind: &v1alpha1.ParamKind{
						APIVersion: "rules.example.com/v1",
						Kind:       "ReplicaLimit",
					},
					Validations: []v1alpha1.Validation{
						{
							Expression: "1 < 'asdf'",
						},
						{
							Expression: "1 < 2",
						},
					},
					MatchConstraints: &v1alpha1.MatchResources{
						MatchPolicy: func() *v1alpha1.MatchPolicyType {
							r := v1alpha1.MatchPolicyType("Exact")
							return &r
						}(),
						ResourceRules: []v1alpha1.NamedRuleWithOperations{
							{
								RuleWithOperations: v1alpha1.RuleWithOperations{
									Operations: []v1.OperationType{"CREATE"},
									Rule: v1.Rule{
										APIGroups:   []string{"a"},
										APIVersions: []string{"a"},
										Resources:   []string{"a"},
									},
								},
							},
						},
						ObjectSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"a": "b"},
						},
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"a": "b"},
						},
					},
				},
			},
			errorExpressions: map[string]string{
				"1 < 'asdf'": "found no matching overload for '_<_' applied to '(int, string)",
			},
		},
		{
			name: "valid syntax",
			policy: &v1alpha1.ValidatingAdmissionPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
				},
				Spec: v1alpha1.ValidatingAdmissionPolicySpec{
					FailurePolicy: func() *v1alpha1.FailurePolicyType {
						r := v1alpha1.FailurePolicyType("Fail")
						return &r
					}(),
					Validations: []v1alpha1.Validation{
						{
							Expression: "1 < 2",
						},
						{
							Expression: "object.spec.string.matches('[0-9]+')",
						},
						{
							Expression: "request.kind.group == 'example.com' && request.kind.version == 'v1' && request.kind.kind == 'Fake'",
						},
					},
					MatchConstraints: &v1alpha1.MatchResources{
						MatchPolicy: func() *v1alpha1.MatchPolicyType {
							r := v1alpha1.MatchPolicyType("Exact")
							return &r
						}(),
						ResourceRules: []v1alpha1.NamedRuleWithOperations{
							{
								RuleWithOperations: v1alpha1.RuleWithOperations{
									Operations: []v1.OperationType{"CREATE"},
									Rule: v1.Rule{
										APIGroups:   []string{"a"},
										APIVersions: []string{"a"},
										Resources:   []string{"a"},
									},
								},
							},
						},
						ObjectSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"a": "b"},
						},
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"a": "b"},
						},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c CELValidatorCompiler
			validator, err := c.Compile(tc.policy)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if validator == nil {
				t.Fatalf("unexpected nil validator")
			}
			validations := tc.policy.Spec.Validations
			CompilationResults := validator.(*CELValidator).compilationResults
			require.Equal(t, len(validations), len(CompilationResults))

			meets := make([]bool, len(validations))
			for expr, expectErr := range tc.errorExpressions {
				for i, result := range CompilationResults {
					if validations[i].Expression == expr {
						if result.Error == nil {
							t.Errorf("Expect expression '%s' to contain error '%v' but got no error", expr, expectErr)
						} else if !strings.Contains(result.Error.Error(), expectErr) {
							t.Errorf("Expected validation '%s' error to contain '%v' but got: %v", expr, expectErr, result.Error)
						}
						meets[i] = true
					}
				}
			}
			for i, meet := range meets {
				if !meet && CompilationResults[i].Error != nil {
					t.Errorf("Unexpected err '%v' for expression '%s'", CompilationResults[i].Error, validations[i].Expression)
				}
			}
		})
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name            string
		policy          *v1alpha1.ValidatingAdmissionPolicy
		attributes      admission.Attributes
		params          *unstructured.Unstructured
		PolicyDecisions []PolicyDecision
	}{
		{
			name: "valid syntax",
			policy: &v1alpha1.ValidatingAdmissionPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
				},
				Spec: v1alpha1.ValidatingAdmissionPolicySpec{
					FailurePolicy: func() *v1alpha1.FailurePolicyType {
						r := v1alpha1.FailurePolicyType("Fail")
						return &r
					}(),
					Validations: []v1alpha1.Validation{
						{
							Expression: "has(object.subsets) && object.subsets.size() < 2",
						},
					},
					MatchConstraints: &v1alpha1.MatchResources{
						MatchPolicy: func() *v1alpha1.MatchPolicyType {
							r := v1alpha1.MatchPolicyType("Exact")
							return &r
						}(),
						ResourceRules: []v1alpha1.NamedRuleWithOperations{
							{
								RuleWithOperations: v1alpha1.RuleWithOperations{
									Operations: []v1.OperationType{"CREATE"},
									Rule: v1.Rule{
										APIGroups:   []string{"a"},
										APIVersions: []string{"a"},
										Resources:   []string{"a"},
									},
								},
							},
						},
						ObjectSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"a": "b"},
						},
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"a": "b"},
						},
					},
				},
			},
			attributes: newValidAttribute(),
			PolicyDecisions: []PolicyDecision{
				{
					Kind: Admit,
				},
			},
		},
		{
			name: "valid syntax for request",
			policy: &v1alpha1.ValidatingAdmissionPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
				},
				Spec: v1alpha1.ValidatingAdmissionPolicySpec{
					FailurePolicy: func() *v1alpha1.FailurePolicyType {
						r := v1alpha1.FailurePolicyType("Fail")
						return &r
					}(),
					Validations: []v1alpha1.Validation{
						{
							Expression: "request.operation == 'CREATE'",
						},
					},
					MatchConstraints: &v1alpha1.MatchResources{
						MatchPolicy: func() *v1alpha1.MatchPolicyType {
							r := v1alpha1.MatchPolicyType("Exact")
							return &r
						}(),
						ResourceRules: []v1alpha1.NamedRuleWithOperations{
							{
								RuleWithOperations: v1alpha1.RuleWithOperations{
									Operations: []v1.OperationType{"CREATE"},
									Rule: v1.Rule{
										APIGroups:   []string{"a"},
										APIVersions: []string{"a"},
										Resources:   []string{"a"},
									},
								},
							},
						},
						ObjectSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"a": "b"},
						},
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"a": "b"},
						},
					},
				},
			},
			attributes: newValidAttribute(),
			PolicyDecisions: []PolicyDecision{
				{
					Kind: Admit,
				},
			},
		},
		{
			name: "valid syntax for configMap",
			policy: &v1alpha1.ValidatingAdmissionPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
				},
				Spec: v1alpha1.ValidatingAdmissionPolicySpec{
					FailurePolicy: func() *v1alpha1.FailurePolicyType {
						r := v1alpha1.FailurePolicyType("Fail")
						return &r
					}(),
					ParamKind: &v1alpha1.ParamKind{
						APIVersion: "v1",
						Kind:       "ConfigMap",
					},
					Validations: []v1alpha1.Validation{
						{
							Expression: "request.namespace != params.data.fakeString",
						},
					},
					MatchConstraints: &v1alpha1.MatchResources{
						MatchPolicy: func() *v1alpha1.MatchPolicyType {
							r := v1alpha1.MatchPolicyType("Exact")
							return &r
						}(),
						ResourceRules: []v1alpha1.NamedRuleWithOperations{
							{
								RuleWithOperations: v1alpha1.RuleWithOperations{
									Operations: []v1.OperationType{"CREATE"},
									Rule: v1.Rule{
										APIGroups:   []string{"a"},
										APIVersions: []string{"a"},
										Resources:   []string{"a"},
									},
								},
							},
						},
						ObjectSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"a": "b"},
						},
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"a": "b"},
						},
					},
				},
			},
			attributes: newValidAttribute(),
			params: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"data": map[string]interface{}{
						"fakeString": "fake",
					},
				},
			},
			PolicyDecisions: []PolicyDecision{
				{
					Kind: Admit,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := CELValidatorCompiler{}
			validator, err := c.Compile(tc.policy)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if validator == nil {
				t.Fatalf("unexpected nil validator")
			}
			validations := tc.policy.Spec.Validations
			CompilationResults := validator.(*CELValidator).compilationResults
			require.Equal(t, len(validations), len(CompilationResults))

			// TODO: construct objectInterface for testing
			policyResults, err := validator.Validate(tc.attributes, nil, tc.params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			require.Equal(t, len(policyResults), len(tc.PolicyDecisions))
			for i, policyDecision := range tc.PolicyDecisions {
				if policyDecision.Kind != policyResults[i].Kind {
					t.Errorf("Expected policy decision kind '%v' but got '%v'", policyDecision.Kind, policyResults[i].Kind)
				}
				if policyDecision.Message != policyResults[i].Message {
					t.Errorf("Expected policy decision message '%v' but got '%v'", policyDecision.Message, policyResults[i].Message)
				}
			}
		})
	}
}

func newValidAttribute() admission.Attributes {
	object := &corev1.Endpoints{
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "127.0.0.1"}},
			},
		},
	}
	return admission.NewAttributesRecord(object, nil, schema.GroupVersionKind{}, "default", "foo", schema.GroupVersionResource{}, "", admission.Create, &metav1.CreateOptions{}, false, nil)

}
