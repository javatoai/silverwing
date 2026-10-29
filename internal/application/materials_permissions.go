package application

import (
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

// MaterialsPermissions describes the validated snapshot for presentation only.
// Every backend document action still reloads its task identity and scope.
type MaterialsPermissions = documents.MaterialsDescriptor

func taskMaterialsPermissions(manifest domain.TaskManifest, config domain.AppConfig) MaterialsPermissions {
	description := documents.DescribeMaterials(value(manifest.RequirementMaterials.WriteRoot))
	if validAnnualTaskMaterials(manifest, config) != nil {
		description.ReadRoot = ""
		description.CanWrite, description.CanSync = false, false
	}
	return description
}
