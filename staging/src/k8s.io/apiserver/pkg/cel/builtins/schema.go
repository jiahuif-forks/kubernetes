package builtins

import (
	"fmt"

	commoncel "k8s.io/apiserver/pkg/cel"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

func (c *Compiler) DeclType(s *spec.Schema) (*commoncel.DeclType, error) {
	if len(s.Type) != 1 {
		return nil, ErrMalformedSchema
	}
	switch s.Type[0] {
	case "array":
		if s.Items.Len() == 0 {
			return nil, nil
		}
		itemsType, err := c.DeclType(schemaFromSchemaOrArray(s.Items))
		if err != nil {
			return nil, err
		}
		return commoncel.NewListType(itemsType, 0), nil
	case "object":
		// a. use AdditionalProperties
		if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
			apType, err := c.DeclType(s.AdditionalProperties.Schema)
			if err != nil {
				return nil, err
			}
			if apType == nil {
				return nil, nil
			}
			return commoncel.NewMapType(commoncel.StringType, apType, 0), nil
		}

		// b. use Properties
		if s.Properties != nil {
			required := make(map[string]bool)
			for _, name := range s.Required {
				required[name] = true
			}

			fields := make(map[string]*commoncel.DeclField, len(s.Properties))
			for name, prop := range s.Properties {
				name, ok := commoncel.Escape(name)
				if !ok {
					return nil, fmt.Errorf("%w: unable to escape: %q", ErrMalformedSchema, name)
				}
				fieldType, err := c.DeclType(&prop)
				if err != nil {
					return nil, err
				}
				fields[name] = commoncel.NewDeclField(name, fieldType, required[name], s.Enum, s.Default)
			}
			return commoncel.NewObjectType("object", fields), nil
		}

		return nil, ErrMalformedSchema
	case "string":
		return commoncel.StringType, nil
	case "boolean":
		return commoncel.BoolType, nil
	case "number":
		return commoncel.DoubleType, nil
	case "integer":
		return commoncel.IntType, nil
	}

	return nil, ErrMalformedSchema
}

var ErrSchemaNotFound = fmt.Errorf("schame not found")
var ErrMalformedSchema = fmt.Errorf("%w: malformed schema", ErrInternal)

func schemaFromSchemaOrArray(s *spec.SchemaOrArray) *spec.Schema {
	if s.Schema != nil {
		return s.Schema
	}
	if len(s.Schemas) == 0 {
		return nil
	}
	return &s.Schemas[0]
}
