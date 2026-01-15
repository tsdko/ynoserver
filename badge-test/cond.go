package main

import (
	"fmt"
	"strconv"

	"github.com/ynoproject/ynoserver/server"
)

type StepType int

// maybe TODO: making this a mask could make it serve double duty as a filter for condition checks, maybe
const (
	PrevMapStep StepType = iota
	CoordsStep
	TeleportStep
	EventStep
	PictureStep
	SwitchStep
	VarStep
)

type Step struct {
	Type    StepType
	Ints    []int
	Strings []string
}

// idea: method accessors for actual type-dependent data
// (honestly not sure if this is less or more efficient than type assertions or switches)

type StepVarOp int

const (
	EqVarOp StepVarOp = iota
	NeVarOp
	GtVarOp
	LtVarOp
	GeVarOp
	LeVarOp
	BetweenVarOp
)

func NewStepVarOp(op string) StepVarOp {
	switch op {
	case "=":
		return EqVarOp
	case "!=":
		return NeVarOp
	case "<":
		return LtVarOp
	case ">":
		return GtVarOp
	case "<=":
		return LeVarOp
	case ">=":
		return GeVarOp
	case ">=<":
		return BetweenVarOp
	default:
		panic("unknown op " + op)
	}
}

func (op StepVarOp) Exec(v, o1, o2 int) bool {
	switch op {
	case EqVarOp:
		return v == o1
	case NeVarOp:
		return v != o1
	case LtVarOp:
		return v < o1
	case GtVarOp:
		return v > o1
	case LeVarOp:
		return v <= o1
	case GeVarOp:
		return v >= o1
	case BetweenVarOp:
		return v >= o1 && v < o2
	default:
		panic("unknown op " + strconv.Itoa(int(op)))
	}
}

func switchSteps(trigger int, ids []int, values []bool) []Step {
	steps := []Step{}

	for i := range ids {
		iv := 0
		if values[i] {
			iv = 1
		}
		steps = append(steps, Step{
			Type: SwitchStep,
			Ints: []int{trigger, ids[i], iv},
		})
		// every step past the first one is always with trigger 0
		trigger = 0
	}

	return steps
}

func varSteps(trigger int, ids []int, ops []string, values []int) []Step {
	steps := []Step{}

	for i := range ids {
		steps = append(steps, Step{
			Type: VarStep,
			Ints: []int{trigger, ids[i], int(NewStepVarOp(ops[i])), values[i]},
		})
		// every step past the first one is always with trigger 0
		trigger = 0
	}

	return steps
}

func conditionSteps(c server.Condition) ([]Step, error) {
	// TODO: similar thing for time trials

	steps := make([]Step, 0)

	values := c.Values
	if len(c.Values) == 0 {
		values = []string{c.Value}
	}
	if c.Trigger == "prevMap" {
		intValues := make([]int, len(values))
		for _, sv := range values {
			// XXX it would be great if we could avoid this conversion to begin with so we could avoid returning an error etc
			iv, err := strconv.Atoi(sv)
			if err != nil {
				return nil, fmt.Errorf("convert %s value %v to int: %w", c.Trigger, sv, err)
			}
			intValues = append(intValues, iv)
		}
		steps = append(steps, Step{Type: PrevMapStep, Ints: intValues})
	}

	/*
		// should be handled outside of steps
		if c.Map != 0 {
			s.RoomID = c.Map
		}
	*/
	if c.Trigger == "coords" || c.MapX1 != 0 || c.MapY1 != 0 || c.MapX2 != 0 || c.MapY2 != 0 {
		// TODO: we should also set RoomClient.syncCoords for every joining client if we have any CoordsSteps (TeleportStep not needed, teleports are always sync-checked)
		// (this causes a sync check to be performed on every move instead of on sync packets only)
		// (should this be like a global property of the room / the game so we don't have to deep search every time?)
		t := CoordsStep
		if c.Trigger == "teleport" {
			t = TeleportStep
		}
		rect := []int{c.MapX1, c.MapY1, c.MapX2, c.MapY2}
		steps = append(steps, Step{Type: t, Ints: rect})
	}

	triggerNum := 0
	switch c.Trigger {
	case "eventAction":
		triggerNum = 1
		fallthrough
	case "event":
		intValues := make([]int, 0, len(values)+1)
		intValues = append(intValues, triggerNum)
		for _, sv := range values {
			// XXX it would be great if we could avoid this conversion to begin with so we could avoid returning an error etc
			iv, err := strconv.Atoi(sv)
			if err != nil {
				return nil, fmt.Errorf("convert %s value %v to int: %w", c.Trigger, sv, err)
			}
			intValues = append(intValues, iv)
		}
		steps = append(steps, Step{Type: EventStep, Ints: intValues})
	}
	if c.Trigger == "picture" {
		steps = append(steps, Step{Type: PictureStep, Strings: values})
	}

	triggerNum = 0
	switchIds := c.SwitchIds
	switchValues := c.SwitchValues
	if len(switchIds) == 0 && c.SwitchId != 0 {
		switchIds = []int{c.SwitchId}
		switchValues = []bool{c.SwitchValue}
	}
	var switchInitTrigger int
	if c.Trigger != "" || c.VarTrigger {
		switchInitTrigger = 0
	} else if c.SwitchDelay {
		switchInitTrigger = 1
	} else {
		switchInitTrigger = 2
	}

	varIds := c.VarIds
	varOps := c.VarOps
	varValues := c.VarValues
	if len(varIds) == 0 && c.VarId != 0 {
		varIds = []int{c.VarId}
		varOps = []string{c.VarOp}
		varValues = []int{c.VarValue}
	}
	var varInitTrigger int
	if c.Trigger != "" || (!c.VarTrigger && len(switchIds) > 0) {
		varInitTrigger = 0
	} else if c.VarDelay {
		varInitTrigger = 1
	} else {
		varInitTrigger = 2
	}

	if c.VarTrigger {
		steps = append(steps, varSteps(varInitTrigger, varIds, varOps, varValues)...)
		steps = append(steps, switchSteps(switchInitTrigger, switchIds, switchValues)...)
	} else {
		steps = append(steps, switchSteps(switchInitTrigger, switchIds, switchValues)...)
		steps = append(steps, varSteps(varInitTrigger, varIds, varOps, varValues)...)
	}

	return steps, nil
}
