package oapifly

import (
	"fmt"
	"sort"
	"strings"
)

// SecurityScheme is one way a client authenticates, the OpenAPI Security Scheme Object. It is
// named in Config.SecuritySchemes, and requirements refer to it by that name.
type SecurityScheme struct {
	// Type is apiKey, http, mutualTLS, oauth2 or openIdConnect.
	Type        string `json:"type" yaml:"type"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Name and In place an apiKey scheme's credential: the header, query parameter or cookie
	// (In) of that name.
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	In   string `json:"in,omitempty" yaml:"in,omitempty"`

	// Scheme is an http scheme's authentication scheme, such as bearer or basic, and
	// BearerFormat hints at what a bearer token is.
	Scheme       string `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty" yaml:"bearerFormat,omitempty"`

	// Flows are an oauth2 scheme's flows, spelt as the OAuth Flows Object spells them.
	Flows map[string]interface{} `json:"flows,omitempty" yaml:"flows,omitempty"`
	// OpenIDConnectURL is an openIdConnect scheme's discovery URL.
	OpenIDConnectURL string `json:"openIdConnectUrl,omitempty" yaml:"openIdConnectUrl,omitempty"`
}

// SecurityRequirement is one way to be authorised for an operation: every scheme it names,
// each with the roles the caller must hold there, such as
//
//	oapifly.SecurityRequirement{"BearerAuth": {"domains:read"}}
//
// An operation's requirements are alternatives, and meeting any one of them is enough.
// OpenAPI 3.1 allows roles on any kind of scheme, not just OAuth scopes.
type SecurityRequirement map[string][]string

// Public is the security of an operation that needs no credential, overriding
// Config.Security:
//
//	oapifly.Endpoint{..., Security: oapifly.Public}
var Public = []SecurityRequirement{}

// requirementsOf is the security an operation states, nil when it states none and inherits
// the document's. A scheme with no roles is written with an empty list, never null.
func requirementsOf(requirements []SecurityRequirement) *[]SecurityRequirement {
	if requirements == nil {
		return nil
	}
	out := make([]SecurityRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		written := SecurityRequirement{}
		for scheme, roles := range requirement {
			if roles == nil {
				roles = []string{}
			}
			written[scheme] = roles
		}
		out = append(out, written)
	}
	return &out
}

// parseSecurityTag reads a swaggo @Security annotation into the alternatives it states. A
// line is one or more alternatives joined by ||, an alternative is one or more schemes joined
// by &&, and a scheme may name its roles in brackets:
//
//	@Security BearerAuth[domains:read, domains:write]
//	@Security SessionCookie && CSRFHeader || BearerAuth
func parseSecurityTag(value string) ([]SecurityRequirement, error) {
	var requirements []SecurityRequirement
	for _, alternative := range strings.Split(value, "||") {
		requirement := SecurityRequirement{}
		for _, scheme := range strings.Split(alternative, "&&") {
			scheme = strings.TrimSpace(scheme)
			name, roleList, hasRoles := strings.Cut(scheme, "[")
			name = strings.TrimSpace(name)
			if name == "" || strings.ContainsAny(name, " ]") {
				return nil, fmt.Errorf("%q is not a scheme name", scheme)
			}
			roles := []string{}
			if hasRoles {
				if !strings.HasSuffix(roleList, "]") {
					return nil, fmt.Errorf("%q does not close its roles", scheme)
				}
				for _, role := range strings.Split(strings.TrimSuffix(roleList, "]"), ",") {
					if role = strings.TrimSpace(role); role != "" {
						roles = append(roles, role)
					}
				}
			}
			requirement[name] = roles
		}
		requirements = append(requirements, requirement)
	}
	return requirements, nil
}

// annotatedSecurity is the security an annotated operation states, nil when it has no
// @Security line. Each line adds alternatives.
func annotatedSecurity(route string, lines []string, reg *schemaRegistry) *[]SecurityRequirement {
	if len(lines) == 0 {
		return nil
	}
	var all []SecurityRequirement
	for _, line := range lines {
		requirements, err := parseSecurityTag(line)
		if err != nil {
			reg.warn(Warning{Kind: WarnAnnotationUnreadable}, "%s: @Security %s: %v, the line is left out", route, line, err)
			continue
		}
		all = append(all, requirements...)
	}
	if all == nil {
		return nil
	}
	return requirementsOf(all)
}

// reportUndefinedSchemes reports every requirement that names a scheme the document does not
// define: a client reading it could not tell how to authenticate. about is the operation
// the requirements are, empty for Config.Security.
func reportUndefinedSchemes(reg *schemaRegistry, about Warning, where string, requirements []SecurityRequirement, schemes map[string]SecurityScheme) {
	for _, requirement := range requirements {
		names := make([]string, 0, len(requirement))
		for name := range requirement {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if _, ok := schemes[name]; !ok {
				w := about
				w.Kind, w.Scheme = WarnSecuritySchemeUndefined, name
				reg.warn(w, "%s: security scheme %q is not defined in Config.SecuritySchemes", where, name)
			}
		}
	}
}

func sortedPaths(paths map[string]map[string]PathItem) []string {
	out := make([]string, 0, len(paths))
	for path := range paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}
