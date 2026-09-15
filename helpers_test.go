package hypermatch

func equalsP(v string) Pattern   { return Pattern{Type: PatternEquals, Value: v} }
func prefixP(v string) Pattern   { return Pattern{Type: PatternPrefix, Value: v} }
func suffixP(v string) Pattern   { return Pattern{Type: PatternSuffix, Value: v} }
func wildcardP(v string) Pattern { return Pattern{Type: PatternWildcard, Value: v} }

func anyOfP(p ...Pattern) Pattern       { return Pattern{Type: PatternAnyOf, Sub: p} }
func allOfP(p ...Pattern) Pattern       { return Pattern{Type: PatternAllOf, Sub: p} }
func anythingButP(p ...Pattern) Pattern { return Pattern{Type: PatternAnythingBut, Sub: p} }

func cond(path string, p Pattern) Condition { return Condition{Path: path, Pattern: p} }

func prop(path string, values ...string) Property { return Property{Path: path, Values: values} }
