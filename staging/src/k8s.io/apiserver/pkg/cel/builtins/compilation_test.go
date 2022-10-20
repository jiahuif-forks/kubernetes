package builtins

import (
	"testing"

	commoncel "k8s.io/apiserver/pkg/cel"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

func TestCompilation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		schemaFunc func(s *spec.Schema)
		rules      []commoncel.ValidationRule
		nilResult  bool
	}{
		{
			name: "valid string",
			schemaFunc: func(s *spec.Schema) {
				s.Type = []string{"string"}
			},
			rules: []commoncel.ValidationRule{
				NewValidationRule("self.startsWith('s')", "should start with 's'"),
			},
		},
		{
			name: "invalid number",
			schemaFunc: func(s *spec.Schema) {
				s.Type = []string{"number"}
			},
			rules: []commoncel.ValidationRule{
				NewValidationRule("size(self) == 10", ""),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := new(Compiler)
			schema := new(spec.Schema)
			tc.schemaFunc(schema)
			d, err := c.DeclType(schema)
			if err != nil {
				t.Fatal(err)
			}
			v, err := c.Compile(tc.rules, d, nil)
			if err != nil {
				t.Fatal(err)
			}
			_ = v
		})
	}
}
