package desktop

// TaskTransition is an immutable selection event. It carries incarnation and
// materials identity, rather than list indexes or mutable task/workspace slices.
type TaskTransition struct {
	previous, next taskSelectionIdentity
	origin         taskTransitionOrigin
	indexChanged   bool
}

type taskSelectionIdentity struct {
	id, path, createdAt, materialsPath string
	archived                           bool
}

type taskTransitionOrigin uint8

const (
	taskTransitionUser taskTransitionOrigin = iota
	taskTransitionSnapshot
)

func newTaskTransition(previous, next task, origin taskTransitionOrigin, indexChanged bool) TaskTransition {
	identity := func(t task) taskSelectionIdentity {
		return taskSelectionIdentity{t.id, t.path, t.createdAt, t.materialsPath, t.archived}
	}
	return TaskTransition{previous: identity(previous), next: identity(next), origin: origin, indexChanged: indexChanged}
}

func (t TaskTransition) changed() bool { return t.previous != t.next }

// The shell coordinates owners; it does not reach into their cancel functions,
// editor fields, or job registries. Reading is captured before presentation reset.
func (s *app) prepareFeatureTaskSelection(transition TaskTransition) error {
	if err := s.materialsUnit.prepareTaskSelection(transition, s); err != nil {
		return err
	}
	if transition.origin == taskTransitionSnapshot {
		s.beforeReadingTaskSelection()
	}
	s.prepareWorkspaceTaskSelection(transition)
	if transition.origin == taskTransitionUser {
		s.markReadingInteraction()
		s.beforeReadingTaskSelection()
	}
	s.materialsUnit.leaveTaskSelection(transition, s)
	s.prepareTaskDialogSelection(transition)
	return nil
}
