package builtins

import (
	"sync"

	"github.com/google/cel-go/cel"

	"k8s.io/apiserver/pkg/cel/library"
)

var (
	initEnvOnce sync.Once
	initEnv     *cel.Env
	initEnvErr  error
)

func getBaseEnv() (*cel.Env, error) {
	initEnvOnce.Do(func() {
		var opts []cel.EnvOption
		opts = append(opts, cel.HomogeneousAggregateLiterals())
		// Validate function declarations once during base env initialization,
		// so they don't need to be evaluated each time a CEL rule is compiled.
		// This is a relatively expensive operation.
		opts = append(opts, cel.EagerlyValidateDeclarations(true), cel.DefaultUTCTimeZone(true))
		opts = append(opts, library.ExtensionLibs...)

		initEnv, initEnvErr = cel.NewEnv(opts...)
	})
	return initEnv, initEnvErr
}
