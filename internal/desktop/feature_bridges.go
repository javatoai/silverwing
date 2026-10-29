package desktop

import ()

// These view bridges keep rendering and native window code on app while the
// owning feature controller applies state and worker lifecycle rules.

func (s *app) closeTaskCreationRequirements() {
	if s == nil {
		return
	}
	s.taskUnit.closeTaskCreationRequirements()
}

func (s *app) cancelTaskCreationAINaming() {
	if s == nil {
		return
	}
	s.taskUnit.cancelTaskCreationAINaming()
}

func (s *app) closeTaskCreationAINaming() {
	if s == nil {
		return
	}
	s.taskUnit.closeTaskCreationAINaming()
}
