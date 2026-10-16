package desktop

import (
	"silverwing-go/internal/application"
)

func (s *app) taskByID(id string) task {
	for _, item := range s.tasks {
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

func (s *app) serviceByID(groupID, id string) (application.Service, bool) {
	for _, service := range s.snapshot.Services {
		if service.GroupID == groupID && service.ID == id {
			return service, true
		}
	}
	return application.Service{}, false
}
