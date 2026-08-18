package oapifly

import (
	"strings"
)

// Links, and why they are declared rather than inferred.
//
// A Link Object says that one operation's response leads to another operation, and where the
// target's parameters come from:
//
//	responses:
//	  "201":
//	    links:
//	      Read:
//	        operationId: readDevice
//	        parameters: {deviceGuid: "$response.body#/guid"}
//
// That is what lets a consumer - a contract tester, a client generator - follow a create to the
// read of the thing it created, and it is the only way a description can express a lifecycle:
// created, therefore readable; deleted, therefore not.
//
// Nothing here is guessed from the shape of a path. A generator that paired POST /device with
// GET /device/guid/{deviceGuid} because they look related would mint chains the author never
// claimed, and every consequence drawn from one would be unfalsifiable - the description would
// assert a relationship nobody wrote down. So a link exists only where an @Link annotation
// says so.

// parsedLink is one @Link annotation:
//
//	@Link <status> <name> <targetOperationId> [<param>=<expression> ...]
type parsedLink struct {
	Status      string
	Name        string
	OperationID string
	Parameters  map[string]string
}

// parseLink reads one @Link tag value, reporting whether it was well formed.
//
// The expressions are not examined. `$response.body#/guid`, `$request.path.id` and
// `$statusCode` are OpenAPI runtime expressions, evaluated by whoever follows the link, so this
// passes them through verbatim - a generator that tried to validate them could only be wrong
// about a language it does not implement.
func parseLink(value string) (parsedLink, bool) {
	fields := strings.Fields(value)
	if len(fields) < 3 {
		return parsedLink{}, false
	}

	link := parsedLink{Status: fields[0], Name: fields[1], OperationID: fields[2]}

	// The status is not checked for being a number. `default` is a legal response key, and so
	// are the 2XX ranges, so the only honest test of it is whether the operation declares a
	// response under that key - which attachLinks does, and which also catches the fields being
	// written in the wrong order.
	for _, field := range fields[3:] {
		name, expression, found := strings.Cut(field, "=")
		if !found || name == "" || expression == "" {
			return parsedLink{}, false
		}
		if link.Parameters == nil {
			link.Parameters = map[string]string{}
		}
		link.Parameters[name] = expression
	}

	return link, true
}

// attachLinks files each declared link under the response it hangs off.
//
// A link on a status the operation does not declare is reported rather than attached: inventing
// the response to hold it would put a status in the document the handler never returns, and
// dropping it in silence would leave the author believing a chain exists.
func attachLinks(responses map[string]Response, tagValues []string, operation string, reg *schemaRegistry) {
	for _, value := range tagValues {
		link, ok := parseLink(value)
		if !ok {
			reg.warn("%s declares a link this generator cannot read (%q); the form is: @Link <status> <name> <targetOperationId> [<param>=<expression> ...]",
				operation, value)
			continue
		}

		response, declared := responses[link.Status]
		if !declared {
			reg.warn("%s hangs the link %q off a %s response it does not declare, so the link is left out",
				operation, link.Name, link.Status)
			continue
		}

		if response.Links == nil {
			response.Links = map[string]Link{}
		}
		// Two links of the same name on one response are one link: the map keeps the last, so
		// the earlier relationship would vanish with nothing said. Usually a copied annotation
		// whose name was not changed with its target.
		if existing, taken := response.Links[link.Name]; taken {
			reg.warn("%s declares the link %q on its %s response twice, to %q and then to %q; only the last survives",
				operation, link.Name, link.Status, existing.OperationID, link.OperationID)
		}
		response.Links[link.Name] = Link{OperationID: link.OperationID, Parameters: link.Parameters}
		responses[link.Status] = response
	}
}

// reportUnknownLinkTargets reports every link whose target is an operationId the document does
// not declare.
//
// It runs once the whole document is built, because that is the earliest a target can be
// checked: an operation may be linked to from a file parsed before its own. A link to an id
// nothing declares is a chain a consumer cannot follow, and the usual cause is an @ID that was
// renamed on one side only.
func reportUnknownLinkTargets(paths map[string]map[string]PathItem, warn func(string, ...interface{})) {
	// Counted rather than collected into a set: the specification requires an operationId to be
	// unique across the document, and a link naming one that two operations share does not say
	// which of them it means - so a consumer following it picks one, and the description cannot
	// tell it which was intended.
	declared := map[string]int{}
	for _, methods := range paths {
		for _, item := range methods {
			if item.OperationID != "" {
				declared[item.OperationID]++
			}
		}
	}

	for path, methods := range paths {
		for method, item := range methods {
			for status, response := range item.Responses {
				for name, link := range response.Links {
					switch declared[link.OperationID] {
					case 0:
						warn("%s %s declares the link %q on its %s response to the operation %q, which nothing in this document declares",
							strings.ToUpper(method), path, name, status, link.OperationID)
					case 1:
						// The only case that resolves to exactly one operation.
					default:
						warn("%s %s declares the link %q on its %s response to the operation %q, which %d operations declare, so the link does not say which one it means",
							strings.ToUpper(method), path, name, status, link.OperationID, declared[link.OperationID])
					}
				}
			}
		}
	}
}
