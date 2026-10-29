package documents

// MaterialsKind identifies a supported materials layout. It is derived by the
// documents boundary and is never used in place of backend path authorization.
type MaterialsKind string

const (
	MaterialsNone   MaterialsKind = "NONE"
	MaterialsAnnual MaterialsKind = "ANNUAL"
	MaterialsLocal  MaterialsKind = "LOCAL"
)

// MaterialsDescriptor is a transient projection of the same scope that checks
// document permissions. It adds no fields to persisted task manifests.
type MaterialsDescriptor struct {
	Kind          MaterialsKind `json:"kind"`
	ReadRoot      string        `json:"readRoot"`
	OwnedRoot     string        `json:"ownedRoot"`
	TemporaryRoot string        `json:"temporaryRoot"`
	CanWrite      bool          `json:"canWrite"`
	CanSync       bool          `json:"canSync"`
}

func TaskMaterialsScope(owned string) (MaterialsScope, bool) {
	scope, _, ok := resolveMaterialsScope(owned)
	return scope, ok
}

// DescribeMaterials recognizes only layouts enabled in this checkout. A path
// that cannot be parsed retains its existing read root but has no capabilities.
func DescribeMaterials(owned string) MaterialsDescriptor {
	scope, kind, ok := resolveMaterialsScope(owned)
	if !ok {
		return MaterialsDescriptor{Kind: MaterialsNone, ReadRoot: owned}
	}
	return MaterialsDescriptor{Kind: kind, ReadRoot: scope.Root, OwnedRoot: scope.Owned, TemporaryRoot: scope.Temporary, CanWrite: true, CanSync: kind == MaterialsAnnual}
}

func (d MaterialsDescriptor) scope() MaterialsScope {
	return MaterialsScope{Root: d.ReadRoot, Owned: d.OwnedRoot, Temporary: d.TemporaryRoot}
}

func (d MaterialsDescriptor) Readable(path string) bool {
	return d.Kind != MaterialsNone && d.ReadRoot != "" && d.scope().Readable(path)
}

func (d MaterialsDescriptor) Writable(path string, allowRoot bool) bool {
	return d.CanWrite && d.Kind != MaterialsNone && d.scope().Writable(path, allowRoot)
}
