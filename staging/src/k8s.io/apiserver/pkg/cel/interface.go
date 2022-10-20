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
	"context"

	"github.com/google/cel-go/common/types/ref"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

type Schema any

type Validator interface {
	Validate(ctx context.Context, fldPath *field.Path, schema Schema, self, oldSelf any, options *ValidationOptions) error
	TransitionRule() bool
}

// Compiler takes a CEL validation rule and its associated types
type Compiler interface {
	Compile(rules []ValidationRule, declType *DeclType, options *CompileOptions) (Validator, error)
}

type CostEstimator interface {
	Cost() int64
}

type Converter interface {
	DeclType(schema Schema) (*DeclType, error)
	Val(object runtime.Object) (*ref.Val, error)
}

// ValidationRule is a validation that evaluates a CEL expression against
// a resource, with the message to report when the validation fails.
type ValidationRule interface {
	// Rule is the CEL expression to evaluate.
	Rule() string
	// Message is a CEL expression that constructs a message when the validation
	// fails.
	Message() string
}

type ValidationOptions struct {
	// CostBudget is the overall cost budget for runtime CEL validation cost
	// per resource.
	// leave empty to use the default value.
	CostBudge int64
}

type CompileOptions struct {
	// PerCallLimit specify the actual cost limit per CEL validation call.
	// leave empty to use the default value.
	PerCallLimit int64
}
