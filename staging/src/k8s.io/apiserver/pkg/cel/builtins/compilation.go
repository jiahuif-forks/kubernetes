package builtins

import (
	"fmt"
	"strings"
	"sync"
	"time"

	commoncel "k8s.io/apiserver/pkg/cel"
	"k8s.io/apiserver/pkg/cel/library"
	"k8s.io/apiserver/pkg/cel/metrics"
	"k8s.io/kube-openapi/pkg/common"

	"github.com/google/cel-go/cel"
)

type Compiler struct {
	baseEnvInstance *cel.Env
	baseEnvInitOnce sync.Once
	baseEnvErr      error

	definitions map[string]common.OpenAPIDefinition
}

var _ commoncel.Compiler = (*Compiler)(nil)

func (c *Compiler) baseEnv() (*cel.Env, error) {
	c.baseEnvInitOnce.Do(func() {
		c.baseEnvInstance, c.baseEnvErr = createDefaultCELEnv()
	})
	return c.baseEnvInstance, c.baseEnvErr
}

func (c *Compiler) Compile(rules []commoncel.ValidationRule, declType *commoncel.DeclType, options *commoncel.CompileOptions) (commoncel.Validator, error) {
	if len(rules) == 0 {
		return nil, nil
	}

	t := time.Now()
	defer metrics.Metrics.ObserveCompilation(time.Since(t))

	env, err := c.baseEnv()
	if err != nil {
		return nil, err
	}

	reg := commoncel.NewRegistry(env)
	selfTypeName := generateUniqueSelfTypeName()
	ruleTypes, err := commoncel.NewRuleTypes(selfTypeName, declType, reg)
	if err != nil {
		return nil, err
	}
	if ruleTypes == nil { // when there is no associated schema
		return nil, nil
	}
	opts, err := ruleTypes.EnvOptions(env.TypeProvider())
	if err != nil {
		return nil, err
	}
	root, ok := ruleTypes.FindDeclType(selfTypeName)
	if !ok {
		root = declType.MaybeAssignTypeName(selfTypeName)
	}
	opts = append(opts, cel.Variable(ScopedVarName, root.CelType()))
	opts = append(opts, cel.Variable(OldScopedVarName, root.CelType()))
	env, err = env.Extend(opts...)
	if err != nil {
		return nil, err
	}

	programs := make([]compiledProgram, 0, len(rules))
	for _, rule := range rules {
		v, err := c.compileRule(rule, env, nil)
		if err != nil {
			return nil, err
		}
		programs = append(programs, v)
	}
	return programs, nil
}

func (c *Compiler) compileRule(rule commoncel.ValidationRule, env *cel.Env, options *commoncel.CompileOptions) (commoncel.Validator, error) {
	ruleExpression := strings.TrimSpace(rule.Rule())
	if len(ruleExpression) == 0 {
		return nil, nil
	}
	ast, issues := env.Compile(ruleExpression)
	if issues != nil {
		return nil, fmt.Errorf("%w: compilation failed: %s", ErrInvalidRule, issues.String())
	}
	if ast.OutputType() != cel.BoolType {
		return nil, fmt.Errorf("%w: expression does not evaluate to a bool", ErrInvalidRule)
	}
	checkedExpr, err := cel.AstToCheckedExpr(ast)
	if err != nil {
		return nil, fmt.Errorf("%w: CEL internal error: %v", ErrInternal, checkedExpr)
	}
	transitionRule := false
	// check if there is reference to oldSelf
	for _, rm := range checkedExpr.ReferenceMap {
		if rm.Name == OldScopedVarName {
			transitionRule = true
			break
		}
	}

	program, err := env.Program(ast,
		cel.EvalOptions(cel.OptOptimize),
		cel.OptimizeRegex(library.ExtensionLibRegexOptimizations...),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: fail to create program: %v", ErrInvalidRule, err)
	}
	return &Validator{transitionRule: transitionRule, program: program}, nil
}

func createDefaultCELEnv() (*cel.Env, error) {
	var opts []cel.EnvOption
	opts = append(opts, cel.HomogeneousAggregateLiterals())
	// Validate function declarations once during base env initialization,
	// so they don't need to be evaluated each time a CEL rule is compiled.
	// This is a relatively expensive operation.
	opts = append(opts, cel.EagerlyValidateDeclarations(true), cel.DefaultUTCTimeZone(true))
	opts = append(opts, library.ExtensionLibs...)

	return cel.NewEnv(opts...)
}

// generateUniqueSelfTypeName creates a placeholder type name to use in a CEL programs for cases
// where we do not wish to expose a stable type name to CEL validator rule authors. For this to effectively prevent
// developers from depending on the generated name (i.e. using it in CEL programs), it must be changed each time a
// CRD is created or updated.
func generateUniqueSelfTypeName() string {
	return fmt.Sprintf("selfType%d", time.Now().Nanosecond())
}

const (
	// ScopedVarName is the variable name assigned to the locally scoped data element of a CEL validation
	// expression.
	ScopedVarName = "self"

	// OldScopedVarName is the variable name assigned to the existing value of the locally scoped data element of a
	// CEL validation expression.
	OldScopedVarName = "oldSelf"
)

var ErrInvalidRule = fmt.Errorf("a rule is invalid")
var ErrInternal = fmt.Errorf("cel internal error")
