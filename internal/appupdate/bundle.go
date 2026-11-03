package appupdate

import (
	"errors"
	"path/filepath"
)

func ReadBundle(directory string, installation Installation, version string) (Metadata, error) {
	var metadata Metadata
	if err := ReadJSON(filepath.Join(directory, MetadataName), &metadata); err != nil {
		return metadata, err
	}
	if metadata.Schema != 1 || !SameProduct(metadata.Identity, installation.Identity) || metadata.Identity.Version != version || metadata.Kind != installation.Kind {
		return metadata, errors.New("更新包的产品、版本或安装形式不匹配")
	}
	root, err := Resolve(directory, metadata.Payload)
	if err != nil {
		return metadata, err
	}
	if metadata.Kind == MSI || metadata.Kind == Setup {
		hash, err := Digest(root)
		if err != nil || hash != metadata.InstallerSHA256 {
			return metadata, errors.New("安装包校验失败")
		}
		if metadata.Kind == MSI && (CanonicalGUID(metadata.ProductCode) == "" || CanonicalGUID(metadata.UpgradeCode) == "" || CanonicalGUID(metadata.UpgradeCode) != CanonicalGUID(installation.UpgradeCode)) {
			return metadata, errors.New("MSI 产品身份不匹配")
		}
	} else {
		if metadata.Layout == nil || metadata.Layout.Identity != metadata.Identity {
			return metadata, errors.New("更新包缺少正确的程序清单")
		}
		if err := VerifyLayout(root, *metadata.Layout); err != nil {
			return metadata, err
		}
	}
	return metadata, nil
}
