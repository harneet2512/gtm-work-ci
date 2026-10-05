package coalesce

// PersistState exposes persistState to the external test package.
var PersistState = persistState

// LoadKnownPeople exposes loadKnownPeople to the external test package.
var LoadKnownPeople = loadKnownPeople

// ParsePGTextArray exposes the array scanner to the external test package.
func ParsePGTextArray(src any) ([]string, error) {
	var a pgTextArray
	if err := a.Scan(src); err != nil {
		return nil, err
	}
	return a, nil
}
