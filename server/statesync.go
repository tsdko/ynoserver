package server

import (
	"errors"
	"fmt"
	"strconv"
)

//type SyncType int

// keep in mind interfaces have allocation overhead (dynamic dispatch too but might not be that important here)
// also having two different approaches to polymorphism in the same file smells a bit
type SyncTarget interface {
	FinishSync(c *RoomClient) error
}

// todo: disconnectsync for e.g. the 2kki debug switch?
// or maybe just make sync target a func pointer instead?

type MinigameSync struct {
	roomMinigameId int
	minigame       *Minigame
}

func (m MinigameSync) FinishSync(c *RoomClient) error {
	if c.varCache[m.minigame.VarId] < c.minigameScores[m.roomMinigameId] {
		return nil
	}
	_, err := tryWritePlayerMinigameScore(c.session.uuid, m.minigame.Id, c.varCache[m.minigame.VarId])
	return err
}

type TagSync struct{ Name string }

func (t TagSync) FinishSync(c *RoomClient) error {
	// TODO (for all sync targets, not just here): recheck all steps with cached values once you reach the last step
	success, err := tryWritePlayerTag(c.session.uuid, t.Name)
	if err != nil {
		return err
	}
	if success {
		c.outbox <- buildMsg("b")
	}
	return nil
}

// XXX: this would have to be uninjected and reinjected on vending machine change, seems ugly
type VmSync struct{}

func (v VmSync) FinishSync(c *RoomClient) error {
	// TODO: implement

	// ideally we wouldn't have to do this but this would mean removing this from the room sync list on vending machine change; right now we don't even support modifying that set live (clients are supposed to hold separate slices of indices into that set)
	if c.room.id != currentEventVmMapId {
		return errors.New("event vm room id mismatch")
	}

	// expected to be tested prior to this via event steps
	/*
		eventIdInt, err := strconv.Atoi(msg[1])
		if err != nil {
			return err
		}

		if !slices.Contains(currentEventVmGroup, eventIdInt) {
			return errors.New("event vm id mismatch")
		}
	*/

	// XXX: we don't have access to the specific event id from here
	/*
		exp, err := tryCompleteEventVm(c.session.uuid, currentEventVmMapId, eventIdInt)
		if err != nil {
			return err
		}
		if exp > -1 {
			c.session.outbox <- buildMsg("vm", exp)
		}
	*/
	return errors.New("unimplemented")
}

type TimeTrialSync struct{ TimeVar int }

func (t TimeTrialSync) FinishSync(c *RoomClient) error {
	value := c.varCache[t.TimeVar]
	if value >= 3600 {
		return nil
	}
	if c.notifiedMaps == nil {
		c.notifiedMaps = make(map[int]bool)
	}
	if !c.notifiedMaps[c.room.id] {
		c.session.outbox <- buildMsg("ttr", c.room.id, value)
		c.notifiedMaps[c.room.id] = true
	}
	success, err := tryWritePlayerTimeTrial(c.session.uuid, c.room.id, value)
	if err != nil {
		return err
	}
	if success {
		c.outbox <- buildMsg("b")
	}
	return nil
}

type Sync struct {
	Target   SyncTarget
	Steps    []Step
	MinLevel int
}

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

// TODO: coords, switches, vars should be rechecked at the end of the chain to ensure they haven't changed between steps (this is already done in the existing prod implementation, not only at the end of the chain though)

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
	TrueVarOp
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
	case "true": // XXX
		return TrueVarOp
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
	case TrueVarOp:
		return true
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

func minigameSteps(minigame *Minigame) []Step {
	// TODO elsewhere: for minigames current player highscores are retrieved on room join

	triggerNum := 1
	if minigame.InitialVarSync {
		triggerNum = 2
	}

	// TODO: dev check for dev minigames

	// ...var value for comparison retrieved later from var cache I guess
	steps := varSteps(triggerNum, []int{minigame.VarId}, []string{"true"}, []int{0}) // XXX ugly
	if minigame.SwitchId > 0 {
		steps = append(steps, switchSteps(0, []int{minigame.SwitchId}, []bool{minigame.SwitchValue})...)
	}
	return steps
}

func timeTrialSteps(game string, secs int) []Step {
	if game != "2kki" {
		panic("unsupported game for time trial: " + game)
	}

	// TODO: if there are extra condition steps, they should be put before the time-trial-specific ones
	steps := switchSteps(0, []int{1430}, []bool{true})
	steps = append(steps, varSteps(0, []int{88}, []string{"true"}, []int{0})...) // XXX ugly
	return steps
}

func conditionSteps(c Condition) ([]Step, error) {
	// TODO: make sure we haven't missed condition fields/values that are unused in current condition files but implemented on the server

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

	// response sent only if the event runs without being triggered by the player
	triggerNum := 0
	switch c.Trigger {
	case "eventAction":
		// response sent only if the player interacts with the event
		// right now this applies to any number above 1 as well
		triggerNum = 1
		fallthrough
	case "event":
		// TODO: current implementation skips this step if there's a vending machine exped in this room with the same event id
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
		// response sent immediately
		switchInitTrigger = 0
	} else if c.SwitchDelay {
		// response sent every time the switch is written to
		switchInitTrigger = 1
	} else {
		// response sent immediately and then every time the switch is written to
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
		// response sent immediately
		varInitTrigger = 0
	} else if c.VarDelay {
		// response sent every time the var is written to
		varInitTrigger = 1
	} else {
		// response sent immediately and then every time the var is written to
		varInitTrigger = 2
	}
	// TODO: if a minigame shares the var id, the minigame trigger takes precedence

	if c.VarTrigger {
		steps = append(steps, varSteps(varInitTrigger, varIds, varOps, varValues)...)
		steps = append(steps, switchSteps(switchInitTrigger, switchIds, switchValues)...)
	} else {
		steps = append(steps, switchSteps(switchInitTrigger, switchIds, switchValues)...)
		steps = append(steps, varSteps(varInitTrigger, varIds, varOps, varValues)...)
	}

	return steps, nil
}
