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

package native

import (
	"bytes"
	"encoding/json"
	"fmt"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/kube-openapi/pkg/common"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

func LoadSchema(GetOpenAPIDefinitions common.GetOpenAPIDefinitions, path string) (*apiextensions.JSONSchemaProps, error) {
	defs := GetOpenAPIDefinitions(func(path string) spec.Ref {
		return spec.MustCreateRef(path)
	})
	s := defs[path].Schema
	err := resolveRefs(defs, &s)
	if err != nil {
		return nil, err
	}
	return toJSONSchemaProps(s)
}

func resolveRefs(defs map[string]common.OpenAPIDefinition, s *spec.Schema) error {
	if s.Ref.GetURL() != nil {
		if d, ok := defs[s.Ref.String()]; ok {
			*s = d.Schema
		} else {
			return fmt.Errorf("referred definition not found: %s", s.Ref.String())
		}
		*s = defs[s.Ref.String()].Schema
	}

	if s.Items != nil {
		if s.Items.Schema != nil {
			err := resolveRefs(defs, s.Items.Schema)
			if err != nil {
				return err
			}
		}
	}

	for n, p := range s.Properties {
		err := resolveRefs(defs, &p)
		if err != nil {
			return err
		}
		s.Properties[n] = p
	}

	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		err := resolveRefs(defs, s.AdditionalProperties.Schema)
		if err != nil {
			return err
		}
	}
	return nil
}

func toJSONSchemaProps(in any) (*apiextensions.JSONSchemaProps, error) {
	b := new(bytes.Buffer)
	err := json.NewEncoder(b).Encode(in)
	if err != nil {
		return nil, err
	}
	s := new(v1.JSONSchemaProps)
	err = json.NewDecoder(b).Decode(s)
	if err != nil {
		return nil, err
	}
	out := new(apiextensions.JSONSchemaProps)
	err = v1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(s, out, nil)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func toUnstructured(whatever any) map[string]interface{} {
	b := new(bytes.Buffer)
	_ = json.NewEncoder(b).Encode(whatever)
	res := make(map[string]interface{})
	_ = json.NewDecoder(b).Decode(&res)
	return res
}
