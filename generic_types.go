package oapifly

import (
	"go/ast"
	"go/token"
	"strings"
)

// Generic types, and why they need their own resolution path.
//
// A generic envelope is how one type serves every payload:
//
//	type Page[T any] struct {
//		Data []T  `json:"data"`
//		Meta Meta `json:"meta"`
//	}
//
// The name in an annotation - `types.Page[types.Item]` - is not the name of any declaration:
// the file declares `Page`, and `Item` arrives separately as the argument. Looking the whole
// string up therefore found nothing, and the type was described as an untyped object under a
// component key containing brackets, which the Components Object forbids.
//
// So an instantiation is resolved in three steps: read the name into a base and its
// arguments, resolve each argument to a schema in the CALLER's scope, then build the base's
// body with the type parameters bound to those schemas. Binding schemas rather than
// substituting syntax is what makes nesting work - the argument is already resolved before
// the body is entered, so `Page[Wrapper[Item]]` needs no special case.

// typeArg is one bound type parameter: the schema its argument resolved to, and the text the
// argument was written as. The text is kept because a body can instantiate another generic
// with the same parameter - `Inner Wrapper[T]` inside `Page[T]` - and naming that
// instantiation needs the argument's name, not its schema.
type typeArg struct {
	text   string
	schema map[string]interface{}
}

// parseGenericName reads a generic instantiation's name into its base and arguments, and
// reports whether it was one. `Page[Item]` is the Go spelling and the one annotations use.
func parseGenericName(name string) (base string, args []string, ok bool) {
	open := strings.Index(name, "[")
	if open <= 0 || !strings.HasSuffix(name, "]") {
		return "", nil, false
	}
	base = name[:open]

	inner := name[open+1 : len(name)-1]
	if strings.TrimSpace(inner) == "" {
		return "", nil, false
	}

	// Split on commas at the top level only: a comma inside a nested instantiation belongs
	// to the inner type, so `Page[Pair[string,Item]]` has ONE argument.
	depth := 0
	var current strings.Builder
	for _, r := range inner {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(current.String()))
				current.Reset()
				continue
			}
		}
		current.WriteRune(r)
	}
	args = append(args, strings.TrimSpace(current.String()))

	for _, arg := range args {
		if arg == "" {
			return "", nil, false
		}
	}
	return base, args, true
}

// genericSchemaName is the component key for an instantiation.
//
// It cannot be the Go spelling: the specification requires every key under components to
// match ^[a-zA-Z0-9.\-_]+$, and `Page[types.Item]` does not - a strict reader rejects the
// whole document. The parts are joined with dashes instead, package prefixes dropped, which
// is the spelling swag settled on for the same problem.
func genericSchemaName(base string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, sanitizeComponentKey(stripPackagePrefix(base)))
	for _, arg := range args {
		parts = append(parts, componentSafeTypeName(arg))
	}
	return strings.Join(parts, "-")
}

// componentSafeTypeName renders one type argument as a component-key fragment.
//
// A pointer argument keeps a name of its own rather than collapsing onto the value's: the two
// instantiations have different schemas - one permits null - and sharing a key would let
// whichever was resolved first answer for both.
func componentSafeTypeName(arg string) string {
	if item, ok := arrayItemType(arg); ok {
		return componentSafeTypeName(item) + "List"
	}
	if inner, ok := pointerItemType(arg); ok {
		return componentSafeTypeName(inner) + "OrNull"
	}
	if base, args, ok := parseGenericName(arg); ok {
		return genericSchemaName(base, args)
	}
	return sanitizeComponentKey(stripPackagePrefix(arg))
}

// sanitizeComponentKey drops the characters a component key may not contain.
//
// Every shape this generator names deliberately - a list, a pointer, a nested instantiation -
// is spelled without them, but a type argument can be anything Go accepts, and a map or a
// func argument would otherwise put brackets or parentheses in a key and make the whole
// document unreadable to a strict consumer. Dropping them keeps the key legal; the schema
// beside it still says what the generator could and could not describe.
func sanitizeComponentKey(name string) string {
	var out strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			out.WriteRune(r)
		}
	}
	return out.String()
}

// pointerItemType reports the type a pointer argument points at, and whether it was one.
func pointerItemType(arg string) (string, bool) {
	if strings.HasPrefix(arg, "*") {
		return arg[len("*"):], true
	}
	return "", false
}

// typeParamNames lists a declaration's type parameters in order. A parameter list can share
// a constraint between several names - `[K, V any]` - so each field contributes all of its.
func typeParamNames(typeSpec *ast.TypeSpec) []string {
	if typeSpec.TypeParams == nil {
		return nil
	}
	var names []string
	for _, field := range typeSpec.TypeParams.List {
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

// inOwnScope describes another declaration's body with the current generic bindings set
// aside.
//
// A type parameter's name is scoped to the declaration that declares it: a parameter called
// Item does not change what the type Item means in some other struct. Leaving the binding in
// place while descending made an ordinary field describe the generic's argument instead of its
// own type.
func (r *schemaRegistry) inOwnScope(typeName, typeFile string) (map[string]interface{}, bool) {
	outer := r.typeArgs
	r.typeArgs = nil
	schema, isStruct := schemaForNamedTypeAST(typeName, typeFile, r)
	r.typeArgs = outer
	return schema, isStruct
}

// resolveGeneric registers the schema for an instantiation and returns its component name.
func (r *schemaRegistry) resolveGeneric(base string, args []string, display string) string {
	name := genericSchemaName(base, args)

	// The name drops package qualifiers, so two different instantiations can reduce to one
	// component. The first one describes both, which is worth knowing about rather than
	// discovering in the document. Seeing the SAME instantiation again is either a second
	// reference or the type reaching itself, and both want the name and nothing else.
	if previous, seen := r.genericOrigins[name]; seen {
		if previous != display {
			r.warn("%s and %s reduce to the same component name %s, so %s describes both; qualify or rename one of them",
				previous, display, name, previous)
		}
		return name
	}
	r.genericOrigins[name] = display

	shortBase := stripPackagePrefix(base)
	typeFile := findTypeFile(shortBase, r.typeDirs)
	if typeFile == "" {
		r.warn("generic type %s is not in any configured TypeDirs, described as an untyped object", display)
		r.schemas[name] = map[string]interface{}{"type": "object"}
		return name
	}

	schema := r.schemaForInstantiation(shortBase, args, typeFile, display)
	if schema == nil {
		schema = map[string]interface{}{"type": "object"}
	}
	r.schemas[name] = schema
	return name
}

// schemaForInstantiation builds the base declaration's body with its type parameters bound to
// the arguments' schemas. It returns nil when there is no honest schema to produce, having
// said why.
func (r *schemaRegistry) schemaForInstantiation(shortBase string, args []string, typeFile, display string) map[string]interface{} {
	f, err := parseFile(typeFile)
	if err != nil {
		return nil
	}
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != shortBase {
				continue
			}

			params := typeParamNames(typeSpec)
			if len(params) == 0 {
				r.warn("%s instantiates %s, which declares no type parameter; described as an untyped object", display, shortBase)
				return nil
			}
			if len(params) != len(args) {
				r.warn("%s names %d argument(s) but %s declares %d type parameter(s); described as an untyped object",
					display, len(args), shortBase, len(params))
				return nil
			}

			// The arguments are resolved BEFORE the body is entered, in the scope that named
			// them rather than the body's, which is what keeps a nested instantiation honest.
			bound := make(map[string]typeArg, len(params))
			for i, param := range params {
				bound[param] = typeArg{text: args[i], schema: r.schemaForTypeText(args[i])}
			}

			previous := r.typeArgs
			r.typeArgs = bound
			schema := r.instantiatedBody(typeSpec)
			r.typeArgs = previous
			return schema
		}
	}
	return nil
}

// instantiatedBody describes a generic declaration's body under the current bindings. Most
// are structs; `type List[T any] []T` is not, and is described as what it is.
func (r *schemaRegistry) instantiatedBody(typeSpec *ast.TypeSpec) map[string]interface{} {
	if structType, ok := typeSpec.Type.(*ast.StructType); ok {
		return buildSchemaFromStructAST(structType, r)
	}
	return resolveFieldTypeAST(typeSpec.Type, r)
}

// schemaForTypeText describes a type written as text, the way a type argument arrives.
func (r *schemaRegistry) schemaForTypeText(text string) map[string]interface{} {
	text = strings.TrimSpace(text)

	if strings.HasPrefix(text, "[]") {
		return map[string]interface{}{"type": "array", "items": r.schemaForTypeText(text[len("[]"):])}
	}
	if inner, ok := pointerItemType(text); ok {
		// A pointer is the same type, absent-able, which OpenAPI 3.0 spells nullable.
		return asNullable(r.schemaForTypeText(inner))
	}
	if base, args, ok := parseGenericName(text); ok {
		return map[string]interface{}{"$ref": "#/components/schemas/" + r.resolveGeneric(base, args, text)}
	}
	if known, ok := qualifiedJSONTypes[text]; ok {
		return copySchema(known)
	}
	if primitive := goIdentToOpenAPIType(stripPackagePrefix(text)); primitive != "object" {
		return map[string]interface{}{"type": primitive}
	}
	return r.refOrObject(stripPackagePrefix(text), text)
}

// genericFieldSchema describes a field whose type is an instantiation - `Page[Item]` written
// in Go rather than in an annotation. A type parameter used as an argument is rendered as the
// argument bound to it, so `Inner Wrapper[T]` inside `Page[Item]` becomes Wrapper-Item.
func (r *schemaRegistry) genericFieldSchema(base ast.Expr, indices []ast.Expr) map[string]interface{} {
	baseName, ok := r.typeExprText(base)
	if !ok {
		r.warn("a generic field's type could not be named, described as an untyped object")
		return map[string]interface{}{"type": "object"}
	}
	args := make([]string, 0, len(indices))
	for _, index := range indices {
		name, ok := r.typeExprText(index)
		if !ok {
			// A map, a func, a channel: legal Go this generator has no spelling for. The
			// field stays an unconstrained object rather than a wrong shape, and says so.
			r.warn("an argument of %s is a type this generator cannot name, described as an untyped object", baseName)
			return map[string]interface{}{"type": "object"}
		}
		args = append(args, name)
	}
	display := baseName + "[" + strings.Join(args, ",") + "]"
	return map[string]interface{}{"$ref": "#/components/schemas/" + r.resolveGeneric(baseName, args, display)}
}

// typeExprText renders a type expression back to the text an instantiation names it by, and
// reports whether it could. A bound type parameter renders as its argument.
func (r *schemaRegistry) typeExprText(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		if arg, bound := r.typeArgs[t.Name]; bound {
			return arg.text, true
		}
		return t.Name, true
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			return t.Sel.Name, true
		}
		return pkg.Name + "." + t.Sel.Name, true
	case *ast.StarExpr:
		// The pointer is kept: it is part of which instantiation this is, and dropping it
		// named Page[*Item] and Page[Item] the same component while their schemas differ.
		inner, ok := r.typeExprText(t.X)
		if !ok {
			return "", false
		}
		return "*" + inner, true
	case *ast.ArrayType:
		inner, ok := r.typeExprText(t.Elt)
		if !ok {
			return "", false
		}
		return "[]" + inner, true
	case *ast.IndexExpr:
		return r.instantiationText(t.X, []ast.Expr{t.Index})
	case *ast.IndexListExpr:
		return r.instantiationText(t.X, t.Indices)
	}
	return "", false
}

func (r *schemaRegistry) instantiationText(base ast.Expr, indices []ast.Expr) (string, bool) {
	baseName, ok := r.typeExprText(base)
	if !ok {
		return "", false
	}
	args := make([]string, 0, len(indices))
	for _, index := range indices {
		name, ok := r.typeExprText(index)
		if !ok {
			return "", false
		}
		args = append(args, name)
	}
	return baseName + "[" + strings.Join(args, ",") + "]", true
}
