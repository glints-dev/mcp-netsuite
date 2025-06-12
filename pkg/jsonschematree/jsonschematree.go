package jsonschematree

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/xeipuuv/gojsonschema"
)

type Schema struct {
	Type       schemaType         `json:"type"`
	Properties map[string]*Schema `json:"properties,omitempty"`
	Items      *Schema            `json:"items,omitempty"`
	Format     string             `json:"format,omitempty"`

	OneOf []*Schema `json:"oneOf,omitempty"`

	ID  string `json:"$id,omitempty"`
	Ref string `json:"$ref,omitempty"`
}

type schemaType []string

func (t schemaType) MarshalJSON() ([]byte, error) {
	if len(t) == 1 {
		return json.Marshal(t[0])
	}

	return json.Marshal([]string(t))
}

func (s *Schema) UnmarshalJSON(data []byte) error {
	var parsedData map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsedData); err != nil {
		return err
	}

	// Construct the Ref field.
	refJSON, ok := parsedData["$ref"]
	if ok {
		var ref string
		if err := json.Unmarshal(refJSON, &ref); err != nil {
			return fmt.Errorf("failed to unmarshal JSON: %w", err)
		}

		s.Ref = ref
	}

	// Construct the Type field.
	propertyTypeSet := make(map[string]struct{})
	propertyTypeJSON, ok := parsedData["type"]
	if ok {
		var propertyType interface{}
		if err := json.Unmarshal(propertyTypeJSON, &propertyType); err != nil {
			return fmt.Errorf("failed to unmarshal JSON: %w", err)
		}

		switch propertyType := propertyType.(type) {
		case []string:
			for _, jsonType := range propertyType {
				propertyTypeSet[jsonType] = struct{}{}
			}
		case string:
			propertyTypeSet[propertyType] = struct{}{}
		default:
			return errors.New("unexpected type for property \"type\"")
		}
	}

	nullableJSON, ok := parsedData["nullable"]
	if ok {
		var nullable bool
		if err := json.Unmarshal(nullableJSON, &nullable); err != nil {
			return fmt.Errorf("failed to unmarshal JSON: %w", err)
		}

		if nullable {
			propertyTypeSet["null"] = struct{}{}
		}
	}

	propertyTypes := []string{}
	for propertyType := range propertyTypeSet {
		propertyTypes = append(propertyTypes, propertyType)
	}
	s.Type = propertyTypes

	// Construct the Properties field.
	propertiesJSON, ok := parsedData["properties"]
	if ok {
		var properties map[string]json.RawMessage
		if err := json.Unmarshal(propertiesJSON, &properties); err != nil {
			return fmt.Errorf("failed to unmarshal JSON: %w", err)
		}

		parsedProperties := make(map[string]*Schema)
		for key, value := range properties {
			var valueAsSchema Schema
			if err := json.Unmarshal(value, &valueAsSchema); err != nil {
				return fmt.Errorf("failed to unmarshal JSON: %w", err)
			}

			parsedProperties[key] = &valueAsSchema
		}

		if len(parsedProperties) != 0 {
			s.Properties = parsedProperties
		}
	}

	// Construct the Items field.
	itemsJSON, ok := parsedData["items"]
	if ok {
		var items Schema
		if err := json.Unmarshal(itemsJSON, &items); err != nil {
			return fmt.Errorf("failed to unmarshal JSON: %w", err)
		}

		s.Items = &items
	}

	// Construct the Format field.
	formatJSON, ok := parsedData["format"]
	if ok {
		var format string
		if err := json.Unmarshal(formatJSON, &format); err != nil {
			return fmt.Errorf("failed to unmarshal JSON: %w", err)
		}

		s.Format = format
	}

	// Construct the OneOf field.
	oneOfJSON, ok := parsedData["oneOf"]
	if ok {
		var oneOf []*Schema
		if err := json.Unmarshal(oneOfJSON, &oneOf); err != nil {
			return fmt.Errorf("failed to unmarshal JSON: %w", err)
		}

		s.OneOf = oneOf
	}

	return nil
}

func (s *Schema) BaseType() string {
	if len(s.Type) == 0 {
		return ""
	}

	var typesWithoutNull []string
	for _, propertyType := range s.Type {
		if propertyType != "null" {
			typesWithoutNull = append(typesWithoutNull, propertyType)
		}
	}

	if len(typesWithoutNull) == 0 {
		return "null"
	} else if len(typesWithoutNull) == 1 {
		return typesWithoutNull[0]
	} else {
		return ""
	}
}

// ResolveReferences resolves all external references in this schema.
func (s *Schema) ResolveReferences(resolver ReferenceResolver) error {
	return s.Walk(&referenceResolverWalker{
		Resolver: resolver,
	})
}

type referenceResolverWalker struct {
	Resolver ReferenceResolver
}

func (w *referenceResolverWalker) Walk(s *Schema) error {
	return s.resolveReference(w.Resolver)
}

// ReferenceResolver resolves references to external schema.
type ReferenceResolver interface {
	Resolve(id string) (*Schema, error)
}

// resolveReference resolves the schema if it's a reference to another schema.
// The schems is mutated.
func (s *Schema) resolveReference(resolver ReferenceResolver) error {
	ref := s.Ref
	if ref == "" {
		return nil
	}

	// Extract the record type from the ref.
	refSchema, err := resolver.Resolve(ref)
	if err != nil {
		return fmt.Errorf(
			"failed to resolve ref \"%s\" using resolver: %w",
			ref,
			err,
		)
	}

	s.Ref = ""
	s.ID = refSchema.ID
	s.Type = refSchema.Type
	s.Properties = refSchema.Properties
	s.Items = refSchema.Items

	return nil
}

// Walk walks through each sub-schema found within the schema, executing the
// walker for each sub-schema. It allows the sub-schema to be mutated.
func (s *Schema) Walk(walker SchemaWalker) error {
	stack := NewStack()
	stack.Push(&stackItem{
		Node: s,
		Path: []string{},
	})

	for !stack.Empty() {
		item := stack.Pop()

		properties := item.Node.Properties
		if properties == nil {
			continue
		}

		for property, schema := range properties {
			if err := walker.Walk(schema); err != nil {
				return fmt.Errorf(
					"failed to resolve json schema reference: %w",
					err,
				)
			}

			if len(schema.OneOf) > 0 {
				for _, alternative := range schema.OneOf {
					if err := walker.Walk(alternative); err != nil {
						return fmt.Errorf(
							"failed to resolve json schema reference: %w",
							err,
						)
					}

					stack.Push(&stackItem{
						Node: alternative,
						Path: append(item.Path, "properties", property, "oneOf"),
					})
				}

				continue
			}

			propertyType := schema.BaseType()
			if propertyType == "" {
				return fmt.Errorf(
					"key \"type\" not found on property \"%s\"",
					property,
				)
			}

			if propertyType == gojsonschema.TYPE_ARRAY {
				items := schema.Items
				if items == nil {
					return fmt.Errorf(
						"key \"items\" not found on property \"%s\"",
						property,
					)
				}

				if err := walker.Walk(items); err != nil {
					return fmt.Errorf(
						"failed to resolve json schema reference: %w",
						err,
					)
				}

				stack.Push(&stackItem{
					Node: items,
					Path: append(item.Path, "properties", property, "items"),
				})

			} else if propertyType == gojsonschema.TYPE_OBJECT {
				stack.Push(&stackItem{
					Node: schema,
					Path: append(item.Path, "properties", property),
				})
			}
		}
	}

	return nil
}

type SchemaWalker interface {
	Walk(schema *Schema) error
}

type stackItem struct {
	Node *Schema
	Path []string
}

type stack []*stackItem

func NewStack() *stack {
	var s []*stackItem
	return (*stack)(&s)
}

func (s *stack) Empty() bool {
	return len(*s) == 0
}

func (s *stack) Pop() *stackItem {
	if len(*s) == 0 {
		return nil
	}
	v := (*s)[len(*s)-1]
	*s = (*s)[:len(*s)-1]
	return v
}

func (s *stack) Push(h *stackItem) {
	*s = append(*s, h)
}

func PrepareDummySchema(dummyType []string) *Schema {
	schemaTypes := schemaType{}
	for _, val := range dummyType {
		schemaTypes = append(schemaTypes, val)
	}

	return &Schema{
		Type:       schemaTypes,
		Properties: nil,
		Items:      nil,
		Format:     "",
		OneOf:      nil,
		ID:         "",
		Ref:        "",
	}
}
