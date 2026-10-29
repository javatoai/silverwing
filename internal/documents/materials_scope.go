package documents

// These editions accept only the existing annual materials layout.
func resolveMaterialsScope(owned string) (MaterialsScope, MaterialsKind, bool) {
	scope, ok := AnnualScope(owned)
	if ok {
		return scope, MaterialsAnnual, true
	}
	return MaterialsScope{}, MaterialsNone, false
}
