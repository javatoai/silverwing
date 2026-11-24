package genbuapi

import (
	"encoding/json"
	"strconv"
)

type pipelineStep struct {
	Step        int    `json:"step"`
	StepName    string `json:"step_name"`
	Status      any    `json:"status"`
	CreatedTime string `json:"created_time,omitempty"`
	Enabled     any    `json:"enabled,omitempty"`
}

type pipelineStatus string

const (
	pipelineStatusInitial  pipelineStatus = "初始"
	pipelineStatusBuilding pipelineStatus = "构建中"
	pipelineStatusSuccess  pipelineStatus = "成功"
	pipelineStatusFailed   pipelineStatus = "失败"
	pipelineStatusUnknown  pipelineStatus = "未知"
)

func (step *pipelineStep) status() pipelineStatus {
	if step == nil {
		return pipelineStatusUnknown
	}
	status := pipelineStatusUnknown
	switch value := step.Status.(type) {
	case float64:
		status = pipelineStatusFromNumber(value)
	case int:
		status = pipelineStatusFromNumber(float64(value))
	case json.Number:
		status = pipelineStatusFromText(value.String())
	case string:
		status = pipelineStatusFromText(value)
	default:
		return status
	}
	if step.Step == buildStepNumber && status == pipelineStatusInitial {
		return pipelineStatusBuilding
	}
	return status
}

func pipelineStatusFromNumber(value float64) pipelineStatus {
	switch value {
	case 0:
		return pipelineStatusInitial
	case 1:
		return pipelineStatusSuccess
	case 2:
		return pipelineStatusFailed
	case 3:
		return pipelineStatusBuilding
	default:
		return pipelineStatusUnknown
	}
}

func pipelineStatusFromText(value string) pipelineStatus {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return pipelineStatusUnknown
	}
	return pipelineStatusFromNumber(number)
}

type genbuStatus struct {
	Service          string         `json:"service"`
	AppID            string         `json:"app_id"`
	Tag              string         `json:"tag"`
	VersionID        int            `json:"version_id"`
	VersionName      string         `json:"version_name"`
	BuildStatus      pipelineStatus `json:"build_status"`
	UATStatus        pipelineStatus `json:"uat_status"`
	ProductionStatus pipelineStatus `json:"production_status"`
	BuildStep        *pipelineStep  `json:"build_step,omitempty"`
	UATStep          *pipelineStep  `json:"uat_step,omitempty"`
	ProductionStep   *pipelineStep  `json:"production_step,omitempty"`
}

type genbuVersion struct {
	VersionID   int    `json:"version_id"`
	VersionName string `json:"version_name"`
	AppName     string `json:"app_name"`
}

type genbuBuildLog struct {
	Service string `json:"service"`
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

type configSnapshot struct {
	Service, Environment, Namespace, UpdateTime string
	Files                                       []configBody
}

type configBody struct {
	Name, Body string
}

type genbuPod struct {
	GroupName     string `json:"group_name"`
	Namespace     string `json:"namespace"`
	Name          string `json:"pod_name"`
	IP            string `json:"pod_ip"`
	AppVersion    string `json:"app_version"`
	RestartCount  int    `json:"restart_count"`
	Phase         string `json:"phase"`
	Ready         bool   `json:"ready"`
	Reason        string `json:"reason,omitempty"`
	CreatedTime   string `json:"created_time"`
	operationID   string
	missingPodID  bool
	scopeMismatch bool
}

type podSnapshot struct {
	Service     string     `json:"service"`
	Environment string     `json:"environment"`
	Pods        []genbuPod `json:"pods"`
}

type PipelineStep = pipelineStep
type PipelineStatus = pipelineStatus
type TagStatus = genbuStatus
type BuildLog = genbuBuildLog
type Version = genbuVersion
type ConfigSnapshot = configSnapshot
type Pod = genbuPod
type PodSnapshot = podSnapshot

const (
	PipelineInitial  = pipelineStatusInitial
	PipelineBuilding = pipelineStatusBuilding
	PipelineSuccess  = pipelineStatusSuccess
	PipelineFailed   = pipelineStatusFailed
)

// PipelineStepStatus returns the normalized status for a parsed pipeline
// step, including the special build-step interpretation.
func PipelineStepStatus(step *PipelineStep) PipelineStatus { return step.status() }

// PipelineStatusFromNumber and PipelineStatusFromText expose the shared
// pipeline-status normalization used by API responses and CLI compatibility.
func PipelineStatusFromNumber(value float64) PipelineStatus { return pipelineStatusFromNumber(value) }
func PipelineStatusFromText(value string) PipelineStatus    { return pipelineStatusFromText(value) }
