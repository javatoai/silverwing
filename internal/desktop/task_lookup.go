package desktop

import (
	"silverwing-go/internal/application"
)

func (s *taskController) taskByID(id string) task {
	index, found := s.taskCatalog().byID[id]
	if !found {
		return task{}
	}
	if index < 0 || index >= len(s.state.tasks) || s.state.tasks[index].id != id {
		// A legacy same-length reorder can preserve the structure stamp. Never
		// return its former candidate as another task; refresh dependent rows too.
		s.invalidateTaskIndex()
		index, found = s.taskCatalog().byID[id]
	}
	if found {
		return s.state.tasks[index]
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
