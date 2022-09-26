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

package builtin

import (
	"context"
	"math"
	"testing"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	apiextensionsopenapi "k8s.io/apiextensions-apiserver/pkg/generated/openapi"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func BenchmarkPodSpecWithCEL(b *testing.B) {
	for i := 0; i < b.N; i++ {
		podSpecDef, err := LoadSchema(apiextensionsopenapi.GetOpenAPIDefinitions, "k8s.io/api/core/v1.PodSpec")
		if err != nil {
			b.Fatal(err)
		}

		structural, err := schema.NewStructural(podSpecDef)

		if err != nil {
			b.Fatal(err)
		}

		structural.Extensions.XValidations = v1.ValidationRules{
			v1.ValidationRule{Rule: "has(self.containers) && self.containers.size() > 0", Message: "containers must not be empty"},
			v1.ValidationRule{Rule: "has(self.restartPolicy) && (self.restartPolicy == 'Always' || self.restartPolicy == 'OnFailure' || self.restartPolicy == 'Never')", Message: "restartPolicy"},
		}

		v := cel.NewValidator(structural, true, math.MaxInt64)

		for i := 0; i < b.N; i++ {
			p := toUnstructured(examplePodSpec())
			errs, _ := v.Validate(context.Background(), field.NewPath("root"), structural, p, p, math.MaxInt64)
			if len(errs) != 0 {
				b.Errorf("unexpected errors: %v", errs)
			}
		}
	}
}
