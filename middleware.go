package oapifly

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// Middleware describes what a middleware adds to every operation it wraps, so it is stated
// once, by the middleware, instead of on each operation and forgotten on some (#25). A router
// adapter keeps it beside the middleware itself, for example a capability check returning
// both its handler wrapper and
//
//	oapifly.Middleware{
//	    Roles:     []string{"domains:write"},
//	    Responses: []oapifly.EndpointResponse{{Status: 403, Type: oapifly.TypeOf[ErrorResponse]()}},
//	}
type Middleware struct {
	// Responses are what the middleware can answer instead of the handler: a 401 for a
	// missing session, a 429 with a Retry-After header. An operation's own response for the
	// same status is kept, and gains the headers it does not state.
	Responses []EndpointResponse

	// Security is the requirement an authenticating middleware enforces, used when the
	// operation states none of its own. Of several, the innermost (last) is used.
	Security []SecurityRequirement

	// Roles are what a capability check requires: added to every scheme of every
	// alternative of the operation's requirement, which is its own, else its middleware's,
	// else Config.Security.
	Roles []string
}

// ResponseHeader is a header a response carries, the OpenAPI Header Object.
type ResponseHeader struct {
	Description string
	Required    bool
	// Type is the header value's Go type, described by reflection; a string when nil.
	Type reflect.Type
}

// Header is a Header Object as written in the document.
type Header struct {
	Description string                 `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool                   `json:"required,omitempty" yaml:"required,omitempty"`
	Schema      map[string]interface{} `json:"schema" yaml:"schema"`
}

func describeHeaders(headers map[string]ResponseHeader, reg *schemaRegistry) map[string]Header {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]Header, len(headers))
	for name, h := range headers {
		schema := map[string]interface{}{"type": "string"}
		if h.Type != nil {
			schema = reg.reflectSchema(h.Type)
		}
		out[name] = Header{Description: h.Description, Required: h.Required, Schema: schema}
	}
	return out
}

// describeResponse writes one registered response.
func describeResponse(r EndpointResponse, reg *schemaRegistry) Response {
	desc := r.Description
	if desc == "" {
		desc = http.StatusText(r.Status)
	}
	resp := Response{Description: desc, Headers: describeHeaders(r.Headers, reg)}
	if r.Type != nil {
		media := r.MediaType
		if media == "" {
			media = "application/json"
		}
		resp.Content = map[string]interface{}{
			media: map[string]interface{}{"schema": reg.reflectSchema(r.Type)},
		}
	}
	return resp
}

// addMiddlewareResponses adds what an operation's middleware can answer to the responses
// the operation states.
func addMiddlewareResponses(responses map[string]Response, middleware []Middleware, reg *schemaRegistry) {
	for _, m := range middleware {
		for _, r := range m.Responses {
			status := strconv.Itoa(r.Status)
			added := describeResponse(r, reg)
			own, stated := responses[status]
			if !stated {
				responses[status] = added
				continue
			}
			for name, h := range added.Headers {
				if _, ok := own.Headers[name]; !ok {
					if own.Headers == nil {
						own.Headers = map[string]Header{}
					}
					own.Headers[name] = h
				}
			}
			responses[status] = own
		}
	}
}

// effectiveSecurity is an operation's requirement once its middleware is applied: its own,
// else its innermost authenticating middleware's, with every capability check's roles added
// to each scheme. nil still inherits the document's default.
func effectiveSecurity(route string, own []SecurityRequirement, middleware []Middleware, defaults []SecurityRequirement, reg *schemaRegistry) []SecurityRequirement {
	security := own
	var roles []string
	for _, m := range middleware {
		if own == nil && m.Security != nil {
			security = m.Security
		}
		roles = append(roles, m.Roles...)
	}
	if len(roles) == 0 {
		return security
	}
	if security == nil {
		security = defaults
	}
	if len(security) == 0 {
		reg.warn("%s: its middleware requires %s, but the operation needs no credential to hold them with",
			route, strings.Join(roles, ", "))
		return security
	}

	out := make([]SecurityRequirement, 0, len(security))
	for _, requirement := range security {
		withRoles := SecurityRequirement{}
		for scheme, held := range requirement {
			withRoles[scheme] = appendMissing(append([]string{}, held...), roles)
		}
		out = append(out, withRoles)
	}
	return out
}

func appendMissing(list, more []string) []string {
	for _, item := range more {
		found := false
		for _, existing := range list {
			if existing == item {
				found = true
				break
			}
		}
		if !found {
			list = append(list, item)
		}
	}
	return list
}

// parseHeaderTag reads swaggo's @Header annotation: the statuses it applies to (one, a
// comma-separated list, or "all"), the value's type, the header's name and a description.
//
//	@Header 429 {integer} Retry-After "Seconds to wait"
func parseHeaderTag(value string) (statuses []string, name string, header Header, ok bool) {
	fields := strings.Fields(value)
	if len(fields) < 3 {
		return nil, "", Header{}, false
	}
	dataType := strings.Trim(fields[1], "{}")
	header = Header{Schema: dataTypeSchema(dataType)}
	if len(fields) > 3 {
		header.Description = strings.Trim(strings.Join(fields[3:], " "), "\"")
	}
	return strings.Split(fields[0], ","), fields[2], header, true
}

// attachHeaders puts annotated headers on the responses they name.
func attachHeaders(route string, lines []string, responses map[string]Response, reg *schemaRegistry) {
	for _, line := range lines {
		statuses, name, header, ok := parseHeaderTag(line)
		if !ok {
			reg.warn("%s: @Header %s needs a status, a {type} and a name, the line is left out", route, line)
			continue
		}
		for _, status := range statuses {
			status = strings.TrimSpace(status)
			targets := []string{status}
			if status == "all" {
				targets = targets[:0]
				for each := range responses {
					targets = append(targets, each)
				}
			}
			for _, target := range targets {
				resp, exists := responses[target]
				if !exists {
					reg.warn("%s: @Header %s names response %s, which the operation does not describe", route, name, target)
					continue
				}
				if resp.Headers == nil {
					resp.Headers = map[string]Header{}
				}
				resp.Headers[name] = header
				responses[target] = resp
			}
		}
	}
}
