package delivery

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

type TagPushStatus struct {
	State string `json:"state"`
	SHA   string `json:"sha,omitempty"`
	Error string `json:"error,omitempty"`
}

type TagObservation struct {
	TagOperationReport
	Git          TagPushStatus     `json:"git"`
	GenbuService string            `json:"genbuService,omitempty"`
	Runtime      *TagRuntimeStatus `json:"runtime,omitempty"`
}

type TagRuntimeState string

const (
	TagRuntimeReady     TagRuntimeState = "READY"
	TagRuntimeNotReady  TagRuntimeState = "NOT_READY"
	TagRuntimeAmbiguous TagRuntimeState = "AMBIGUOUS"
	TagRuntimeUnknown   TagRuntimeState = "UNKNOWN"
)

type TagRuntimeStatus struct {
	State      TagRuntimeState       `json:"state"`
	Namespace  string                `json:"namespace,omitempty"`
	Namespaces []TagRuntimeNamespace `json:"namespaces,omitempty"`
	Error      string                `json:"error,omitempty"`
}

type TagRuntimeNamespace struct {
	Namespace          string   `json:"namespace"`
	Versions           []string `json:"versions"`
	PodCount           int      `json:"podCount"`
	ReadyPods          int      `json:"readyPods"`
	MatchingPods       int      `json:"matchingPods"`
	UnknownVersionPods int      `json:"unknownVersionPods,omitempty"`
}

func (t *TagTask) Preflight(ctx context.Context, selection string) (TagPreflight, error) {
	return t.service.preflightTask(ctx, t.task, selection)
}

func (t *TagTask) Observe(ctx context.Context, operationID string) (TagObservation, error) {
	operation, err := t.service.probeOperation(ctx, t.task, operationID, nil)
	if err != nil {
		return TagObservation{}, err
	}
	service, serviceErr := t.task.config.GroupService(t.task.manifest.GroupID, operation.GroupServiceID)
	name := ""
	if serviceErr == nil && service.GenbuProbeEnabled {
		name = service.GenbuServiceName
	} else {
		operation.GenbuStatus = initialGenbuStatus()
	}
	return TagObservation{
		TagOperationReport: operationReport(operation, t.task.manifest.TaskDirectoryName),
		Git:                t.PushStatus(ctx, operation), GenbuService: name,
	}, ctx.Err()
}

// CheckLiveTagBuild requires this query's result even if another reader won
// the cache update. Cached success alone cannot authorize sending a notice.
func (s *Service) CheckLiveTagBuild(ctx context.Context, taskFolder string, expected domain.TagOperation) error {
	task, err := s.resolveTask(taskFolder)
	if err != nil {
		return err
	}
	var observed domain.GenbuTagProbeStatus
	current, err := s.probeOperation(ctx, task, expected.OperationID, &observed)
	if err != nil {
		return err
	}
	if current.State != domain.TagOperationSuccess || current.GroupServiceID != expected.GroupServiceID ||
		value(current.Tag) != value(expected.Tag) {
		return errors.New("核对编译期间 Tag 记录已变化，请重试通知")
	}
	if observed.Build != domain.GenbuSuccess || observed.NotFound || observed.FailureReason != nil {
		return errors.New("本批次的 Genbu 编译尚未全部成功，请刷新构建状态")
	}
	return nil
}

// ObserveUATRuntime enriches observations from this task using one Pod query
// per eligible Genbu service. Each operation is compared against its own Tag.
func (t *TagTask) ObserveUATRuntime(ctx context.Context, observations []TagObservation, namespace string) {
	type podQuery struct {
		pods production.ServicePods
		err  error
	}
	genbu := production.CLIGenbu{Runner: t.service.runner}
	queries := make(map[string]podQuery)
	namespace = strings.TrimSpace(namespace)
	for i := range observations {
		observation := &observations[i]
		observation.Runtime = nil
		operation := observation.Operation
		status := operation.GenbuStatus
		service, tag := strings.TrimSpace(observation.GenbuService), value(operation.Tag)
		if observation.Git.State != "PUSHED" || service == "" || !domain.TagGenbuProbeEligible(operation.State, tag) ||
			status.UAT != domain.GenbuSuccess || status.CheckedAt == nil || status.FailureReason != nil || status.NotFound {
			continue
		}
		query, found := queries[service]
		if !found {
			query.pods, query.err = genbu.Pods(ctx, t.task.config, "uat", service)
			if query.err == nil && query.pods.Service != service {
				query.err = fmt.Errorf("Genbu 返回的 Pod 服务为 %q，当前配置为 %q", query.pods.Service, service)
			}
			queries[service] = query
		}
		if query.err != nil {
			observation.Runtime = &TagRuntimeStatus{State: TagRuntimeUnknown, Namespace: namespace, Error: query.err.Error()}
			continue
		}
		observation.Runtime = summarizeTagRuntime(query.pods.Pods, tag, namespace)
	}
}

func summarizeTagRuntime(pods []production.RuntimePod, tag, namespace string) *TagRuntimeStatus {
	status := &TagRuntimeStatus{State: TagRuntimeNotReady, Namespace: namespace}
	if len(pods) == 0 {
		status.Error = "Genbu 未返回 UAT Pod"
		return status
	}
	byNamespace := make(map[string]*TagRuntimeNamespace)
	for _, pod := range pods {
		evidence := byNamespace[pod.Namespace]
		if evidence == nil {
			evidence = &TagRuntimeNamespace{Namespace: pod.Namespace, Versions: []string{}}
			byNamespace[pod.Namespace] = evidence
		}
		evidence.PodCount++
		if pod.Ready {
			evidence.ReadyPods++
		}
		if strings.TrimSpace(pod.Version) == "" {
			evidence.UnknownVersionPods++
		} else {
			evidence.Versions = append(evidence.Versions, pod.Version)
			if pod.Version == tag {
				evidence.MatchingPods++
			}
		}
	}
	for _, evidence := range byNamespace {
		slices.Sort(evidence.Versions)
		evidence.Versions = slices.Compact(evidence.Versions)
		status.Namespaces = append(status.Namespaces, *evidence)
	}
	slices.SortFunc(status.Namespaces, func(a, b TagRuntimeNamespace) int { return strings.Compare(a.Namespace, b.Namespace) })
	if namespace == "" {
		if len(status.Namespaces) == 1 {
			namespace = status.Namespaces[0].Namespace
		} else {
			for _, evidence := range status.Namespaces {
				if !evidence.ready() {
					status.State, status.Error = TagRuntimeAmbiguous, "多个 UAT namespace 的 Pod 未全部运行目标 Tag 并就绪，请用 --namespace 指定"
					return status
				}
			}
			status.State = TagRuntimeReady
			return status
		}
	}
	status.Namespace = namespace
	evidence := byNamespace[namespace]
	switch {
	case evidence == nil:
		status.Error = fmt.Sprintf("Genbu 未返回 namespace %q 的 UAT Pod", namespace)
	case evidence.ready():
		status.State = TagRuntimeReady
	case evidence.UnknownVersionPods > 0:
		status.State, status.Error = TagRuntimeUnknown, "UAT Pod 缺少运行版本"
	default:
		status.Error = "UAT Pod 未全部运行目标 Tag 并就绪"
	}
	return status
}

func (e TagRuntimeNamespace) ready() bool {
	return e.PodCount == e.ReadyPods && e.PodCount == e.MatchingPods
}

func (t *TagTask) PushStatus(ctx context.Context, operation domain.TagOperation) TagPushStatus {
	if value(operation.Tag) == "" {
		return TagPushStatus{State: "NOT_CREATED"}
	}
	sha, err := t.remoteTagCommit(ctx, operation)
	if err != nil {
		return TagPushStatus{State: "UNKNOWN", Error: err.Error()}
	}
	if sha == "" {
		return TagPushStatus{State: "NOT_PUSHED"}
	}
	expected := value(operation.TargetSHA)
	if operation.TagMode == domain.TagBuildCurrentBranch {
		expected = value(operation.SourceSHA)
	}
	if sha != expected {
		return TagPushStatus{State: "MISMATCH", SHA: sha, Error: fmt.Sprintf("远端 Tag 指向 %s，记录提交为 %s", sha, expected)}
	}
	return TagPushStatus{State: "PUSHED", SHA: sha}
}

func (t *TagTask) remoteTagCommit(ctx context.Context, operation domain.TagOperation) (string, error) {
	target, err := domain.ResolveHistoricalTagTarget(t.task.config, t.task.manifest, operation)
	if err != nil {
		return "", err
	}
	validated, err := t.service.validateForMutation(ctx, t.task, target.Workspace)
	if err != nil {
		return "", err
	}
	return t.service.remoteTagSHA(ctx, validated.repository, target.Remote, value(operation.Tag))
}
