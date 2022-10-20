package builtins

import (
	"context"

	"github.com/google/cel-go/cel"

	"k8s.io/apimachinery/pkg/util/validation/field"
	commoncel "k8s.io/apiserver/pkg/cel"
)

type compiledProgram struct {
	transitionRule bool
	cel.Program
}

func (c *compiledProgram) TransitionRule() bool {
	return c.transitionRule
}

type Validator struct {
	transitionRule bool
	program        cel.Program
}

func (v *Validator) Validate(ctx context.Context, fldPath *field.Path, schema commoncel.Schema, self, oldSelf any, options *commoncel.ValidationOptions) error {
	//TODO implement me
	panic("implement me")
}

func (v *Validator) TransitionRule() bool {
	return v.transitionRule
}

type ValidationRule struct {
	expression string
	message    string
}

func (v *ValidationRule) Rule() string {
	return v.expression
}

func (v *ValidationRule) Message() string {
	return v.message
}

func NewValidationRule(expression, message string) *ValidationRule {
	return &ValidationRule{expression: expression, message: message}
}
