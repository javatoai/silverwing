package desktop

import (
	"silverwing-go/internal/application"
)

func (s *taskController) taskByID(id string) task {
	for _, item := range s.state.tasks {
		if item.id == id {
			return item
		}
	}
	return task{}
}

func (s *app) taskGroup(id string) application.Group {
	for _, group := range s.snapshot.Groups {
		if group.ID == id {
			return group
		}
	}
	return application.Group{}
}
