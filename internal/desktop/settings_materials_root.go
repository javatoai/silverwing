package desktop

func (s *app) saveMaterialsRoot(path string) {
	s.act("config.materials-root.save", "", "", "", path, "", nil)
}
