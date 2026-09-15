package hypermatch

// Property is a named field of an event together with its values. An event
// is a slice of properties. Several properties with the same path act like
// one property with all their values, and a property without values is
// treated as absent. Paths are case-sensitive, values are not.
type Property struct {
	Path   string
	Values []string
}
