package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type exactDecimal struct {
	coefficient *big.Int
	scale       int
}

func parseExactDecimal(value any) (*exactDecimal, error) {
	var raw string
	switch number := value.(type) {
	case json.Number:
		raw = number.String()
	case float64:
		raw = fmt.Sprint(number)
	default:
		return nil, errors.New("个人估分不是数字")
	}
	exponent := 0
	if at := strings.IndexAny(raw, "eE"); at >= 0 {
		parsed, err := strconv.Atoi(raw[at+1:])
		if err != nil || parsed > 10000 || parsed < -10000 {
			return nil, errors.New("个人估分不是有限数字")
		}
		exponent = parsed
		raw = raw[:at]
	}
	negative := strings.HasPrefix(raw, "-")
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "+"), "-")
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" {
		return nil, errors.New("个人估分不是有限数字")
	}
	whole, ok := new(big.Int).SetString(parts[0], 10)
	if !ok {
		return nil, errors.New("个人估分不是有限数字")
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
		if fracInt, ok := new(big.Int).SetString(frac, 10); ok {
			whole.Mul(whole, pow10(len(frac)))
			whole.Add(whole, fracInt)
		} else {
			return nil, errors.New("个人估分不是有限数字")
		}
	}
	if negative {
		whole.Neg(whole)
	}
	scale := len(frac) - exponent
	if scale < 0 {
		whole.Mul(whole, pow10(-scale))
		scale = 0
	}
	valueDecimal := &exactDecimal{coefficient: whole, scale: scale}
	valueDecimal.normalize()
	if valueDecimal.coefficient.Sign() < 0 {
		return nil, errors.New("个人估分不能为负数")
	}
	return valueDecimal, nil
}

func pow10(scale int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
}

func (d *exactDecimal) normalize() {
	if d == nil || d.coefficient == nil {
		return
	}
	ten := big.NewInt(10)
	for d.scale > 0 {
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(d.coefficient, ten, remainder)
		if remainder.Sign() != 0 {
			return
		}
		d.coefficient = quotient
		d.scale--
	}
}

func (d *exactDecimal) add(other *exactDecimal) *exactDecimal {
	if d == nil || d.coefficient == nil {
		return other
	}
	if other == nil || other.coefficient == nil {
		return d
	}
	scale := d.scale
	if other.scale > scale {
		scale = other.scale
	}
	left := new(big.Int).Mul(d.coefficient, pow10(scale-d.scale))
	right := new(big.Int).Mul(other.coefficient, pow10(scale-other.scale))
	result := &exactDecimal{coefficient: new(big.Int).Add(left, right), scale: scale}
	result.normalize()
	return result
}

func (d *exactDecimal) cmp(other *exactDecimal) int {
	scale := d.scale
	if other.scale > scale {
		scale = other.scale
	}
	left := new(big.Int).Mul(d.coefficient, pow10(scale-d.scale))
	right := new(big.Int).Mul(other.coefficient, pow10(scale-other.scale))
	return left.Cmp(right)
}

func (d *exactDecimal) String() string {
	if d == nil || d.coefficient == nil {
		return "0"
	}
	negative := d.coefficient.Sign() < 0
	digits := new(big.Int).Abs(d.coefficient).String()
	if d.scale > 0 {
		if len(digits) <= d.scale {
			digits = strings.Repeat("0", d.scale-len(digits)+1) + digits
		}
		at := len(digits) - d.scale
		digits = digits[:at] + "." + digits[at:]
	}
	if negative {
		return "-" + digits
	}
	return digits
}

type personalSchedule struct {
	points *exactDecimal
	start  *int64
	end    *int64
}

type subTaskEstimate struct {
	owners   map[string]bool
	schedule personalSchedule
	finished bool
}

type nodeEstimates struct {
	personal []personalSchedule
	children map[string]subTaskEstimate
}

func personalEstimate(nodes []map[string]any, currentUserKey string, sprint sprintRange) (*exactDecimal, error) {
	unique := map[string]nodeEstimates{}
	for _, node := range nodes {
		key := text(at(node, "basic", "node_key"))
		if key == "" {
			return nil, errors.New("节点缺少有效标识")
		}
		parsed, err := parseNodeEstimates(node, currentUserKey)
		if err != nil {
			return nil, err
		}
		if prior, exists := unique[key]; exists {
			if !reflect.DeepEqual(prior, parsed) {
				return nil, fmt.Errorf("节点 %s 的个人估分明细重复且不一致", key)
			}
			continue
		}
		unique[key] = parsed
	}
	total := &exactDecimal{coefficient: new(big.Int)}
	for _, node := range unique {
		schedules := node.personal
		if len(node.children) > 0 {
			mine := []personalSchedule{}
			childTotal := &exactDecimal{coefficient: new(big.Int)}
			completedZero := false
			for _, child := range node.children {
				if !child.owners[currentUserKey] {
					continue
				}
				points := child.schedule.points
				if len(child.owners) > 1 && points != nil && points.coefficient.Sign() != 0 {
					return nil, errors.New("多人子项缺少个人估分分配，无法计算当前用户估分")
				}
				mine = append(mine, child.schedule)
				if points != nil {
					childTotal = childTotal.add(points)
				}
				if child.finished && points != nil && points.coefficient.Sign() == 0 {
					completedZero = true
				}
			}
			rollup := &exactDecimal{coefficient: new(big.Int)}
			if len(node.personal) == 1 && node.personal[0].points != nil {
				rollup = node.personal[0].points
			}
			if childTotal.cmp(rollup) < 0 && completedZero {
				schedules = node.personal
			} else if childTotal.cmp(rollup) != 0 {
				return nil, errors.New("子项个人估分与节点个人汇总不一致，无法确认明细完整性")
			} else {
				schedules = mine
			}
		}
		for _, schedule := range schedules {
			if schedule.points == nil || schedule.start == nil || schedule.end == nil {
				continue
			}
			if *schedule.start <= sprint.End && *schedule.end >= sprint.Start {
				total = total.add(schedule.points)
			}
		}
	}
	return total, nil
}

func parseNodeEstimates(node map[string]any, currentUserKey string) (nodeEstimates, error) {
	childrenRaw, exists := node["sub_tasks"]
	children, ok := childrenRaw.([]any)
	if !exists || !ok {
		return nodeEstimates{}, errors.New("节点未返回完整子项估分明细")
	}
	parsedChildren := map[string]subTaskEstimate{}
	for _, raw := range children {
		child := object(raw)
		if child == nil {
			return nodeEstimates{}, errors.New("子项估分明细格式无效")
		}
		id := text(child["sub_task_id"])
		if id == "" {
			return nodeEstimates{}, errors.New("sub_task_id 缺少有效文本")
		}
		ownerJSON, ok := child["owner"].(string)
		if !ok || strings.TrimSpace(ownerJSON) == "" {
			return nodeEstimates{}, fmt.Errorf("子项 %s 人员列表格式无效", id)
		}
		var ownersRaw []map[string]any
		if err := json.Unmarshal([]byte(ownerJSON), &ownersRaw); err != nil {
			return nodeEstimates{}, fmt.Errorf("子项 %s 人员列表格式无效", id)
		}
		owners := map[string]bool{}
		for _, owner := range ownersRaw {
			user := text(owner["username"])
			if user == "" {
				return nodeEstimates{}, errors.New("子项人员缺少 username")
			}
			owners[user] = true
		}
		start, err := optionalOffset(child["estimate_start_date"])
		if err != nil {
			return nodeEstimates{}, fmt.Errorf("子项 %s 排期开始时间：%w", id, err)
		}
		end, err := optionalOffset(child["estimate_end_date"])
		if err != nil {
			return nodeEstimates{}, fmt.Errorf("子项 %s 排期结束时间：%w", id, err)
		}
		if start != nil && end != nil && *start > *end {
			return nodeEstimates{}, fmt.Errorf("子项 %s 排期开始时间晚于结束时间", id)
		}
		points, err := optionalPoints(child["points"])
		if err != nil {
			return nodeEstimates{}, err
		}
		finished := false
		if value, has := child["is_finished"]; has && value != nil {
			var isBool bool
			finished, isBool = value.(bool)
			if !isBool {
				return nodeEstimates{}, fmt.Errorf("子项 %s 完成状态无效", id)
			}
		}
		estimate := subTaskEstimate{owners: owners, schedule: personalSchedule{points, start, end}, finished: finished}
		if prior, has := parsedChildren[id]; has && !reflect.DeepEqual(prior, estimate) {
			return nodeEstimates{}, fmt.Errorf("子项 %s 的估分明细重复且不一致", id)
		}
		parsedChildren[id] = estimate
	}
	personalRaw, exists := node["assignee_schedule_list"]
	if !exists {
		return nodeEstimates{}, errors.New("节点缺少个人排期列表")
	}
	var personal []personalSchedule
	if personalRaw != nil {
		schedules, ok := personalRaw.([]any)
		if !ok {
			return nodeEstimates{}, errors.New("节点缺少个人排期列表")
		}
		seen := map[string]bool{}
		for _, raw := range schedules {
			record := object(raw)
			if record == nil {
				return nodeEstimates{}, errors.New("个人排期格式无效")
			}
			assignee := object(record["assignee"])
			user := text(assignee["user_key"])
			if user == "" {
				return nodeEstimates{}, errors.New("个人排期缺少人员标识")
			}
			if user != currentUserKey {
				continue
			}
			info := object(record["schedule_info"])
			if info == nil {
				return nodeEstimates{}, errors.New("个人排期明细格式无效")
			}
			points, err := optionalPoints(info["points"])
			if err != nil {
				return nodeEstimates{}, err
			}
			start, err := optionalMillis(info["estimate_start_time"])
			if err != nil {
				return nodeEstimates{}, err
			}
			end, err := optionalMillis(info["estimate_finish_time"])
			if err != nil {
				return nodeEstimates{}, err
			}
			if start != nil && end != nil && *start > *end {
				return nodeEstimates{}, errors.New("个人排期开始时间晚于结束时间")
			}
			schedule := personalSchedule{points, start, end}
			fingerprint := scheduleFingerprint(schedule)
			if !seen[fingerprint] {
				seen[fingerprint] = true
				personal = append(personal, schedule)
			}
		}
		if len(personal) > 1 {
			return nodeEstimates{}, errors.New("同一节点的个人排期重复且不一致")
		}
	}
	return nodeEstimates{personal: personal, children: parsedChildren}, nil
}

func optionalPoints(value any) (*exactDecimal, error) {
	if value == nil {
		return nil, nil
	}
	return parseExactDecimal(value)
}

func optionalMillis(value any) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	parsed, ok := integer(value)
	if !ok {
		return nil, errors.New("排期时间戳不是有效毫秒整数")
	}
	return &parsed, nil
}

func optionalOffset(value any) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	dateText, ok := value.(string)
	if !ok {
		return nil, errors.New("不是带时区时间")
	}
	if strings.TrimSpace(dateText) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, dateText)
	if err != nil {
		return nil, errors.New("不是有效带时区时间")
	}
	millis := parsed.UnixMilli()
	return &millis, nil
}

func scheduleFingerprint(value personalSchedule) string {
	points := "null"
	if value.points != nil {
		points = value.points.String()
	}
	start, end := "null", "null"
	if value.start != nil {
		start = strconv.FormatInt(*value.start, 10)
	}
	if value.end != nil {
		end = strconv.FormatInt(*value.end, 10)
	}
	return points + "/" + start + "/" + end
}
