package builtins

import (
	"fmt"
	"sync"
	"time"

	commoncel "k8s.io/apiserver/pkg/cel"
	"k8s.io/apiserver/pkg/cel/library"
	"k8s.io/apiserver/pkg/cel/metrics"

	"github.com/google/cel-go/cel"
)

type builtinCompiler struct {
	baseEnvInstance *cel.Env
	baseEnvInitOnce sync.Once
	baseEnvErr      error
}

func (b *builtinCompiler) baseEnv() (*cel.Env, error) {
	b.baseEnvInitOnce.Do(func() {
		b.baseEnvInstance, b.baseEnvErr = createDefaultCELEnv()
	})
	return b.baseEnvInstance, b.baseEnvErr
}

func (b *builtinCompiler) Compile(rules []commoncel.ValidationRule, declType *commoncel.DeclType, options *commoncel.CompileOptions) (commoncel.Validator, error) {
	if len(rules) == 0 {
		return nil, nil
	}

	t := time.Now()
	defer metrics.Metrics.ObserveCompilation(time.Since(t))

	env, err := b.baseEnv()
	if err != nil {
		return nil, err
	}

	reg := commoncel.NewRegistry(env)
	selfTypeName := generateUniqueSelfTypeName()
	ruleTypes, err := commoncel.NewRuleTypes(selfTypeName, declType, reg)
	if err != nil {
		return nil, err
	}
	if ruleTypes == nil { // when there is no associated schame
		return nil, nil
	}
	opts, err := ruleTypes.EnvOptions(env.TypeProvider())
	if err != nil {
		return nil, err
	}
	_ = opts
	return nil, nil
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
