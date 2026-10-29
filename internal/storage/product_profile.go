package storage

import "silverwing-go/internal/productprofile"

func defaultProfileMaterialsRoot(home string) *string {
	root := productprofile.DefaultMaterialsRoot(home)
	if root == "" {
		return nil
	}
	return &root
}
