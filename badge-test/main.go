package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ynoproject/ynoserver/server"
)

type Session struct {
	RoomID  int
	Packets []Packet
}

type Packet struct {
	IsServer  bool // true means we wait for a packet with this content before sending more stuff
	IsSession bool
	Data      []string
}

func suitableValue(varOp string, value string) string {
	// TODO: return full ranges of valid values (the range being signed 32-bit as supported by easyrpg?)
	// (checking them all will probably be prohibitively heavy if combined with more than one variable range check)
	switch varOp {
	case ">=", "<=", ">=<", "=":
		return value
	}

	i, err := strconv.Atoi(value)
	if err != nil {
		panic(err)
	}
	switch varOp {
	case "!=":
		fallthrough
	case ">":
		return strconv.Itoa(i + 1)
	case "<":
		return strconv.Itoa(i - 1)
	}
	panic("unsupported op " + varOp)
}

func packetsForSwitches(c *server.Condition, switchIds []int, switchValues []bool) []Packet {
	packets := []Packet{}
	triggerNum := 0
	for i := range switchIds {
		if c.Trigger != "" || c.VarTrigger || i > 0 {
			triggerNum = 0
		} else if c.SwitchDelay {
			triggerNum = 1
		} else {
			triggerNum = 2
		}

		packets = append(packets, Packet{
			IsServer: true,
			Data:     []string{"ss", strconv.Itoa(switchIds[i]), strconv.Itoa(triggerNum)},
		})

		strVal := "0"
		if switchValues[i] {
			strVal = "1"
		}
		// TODO: test different orders?
		packets = append(packets, Packet{
			Data: []string{"ss", strconv.Itoa(switchIds[i]), strVal},
		})
	}
	return packets
}

func packetsForVars(c *server.Condition, varIds []int, varOps []string, varValues []int) []Packet {
	packets := []Packet{}
	triggerNum := 0
	// TODO: skip sync packet if it's a registered minigame var (sounds jank)

	for i := range varIds {
		// FIXME: make this initial middle condition less messy?
		//        it's basically "use 0 if we have synced the switches for this condition already"
		if c.Trigger != "" || (!c.VarTrigger && (c.SwitchId != 0 || len(c.SwitchIds) > 0)) || i > 0 {
			triggerNum = 0
		} else if c.VarDelay {
			triggerNum = 1
		} else {
			triggerNum = 2
		}

		// these come in a sequence - you only get sv for var 0 first, you send var 0,
		// then you get asked for var 1, you send var 1 then you get asked for var 2 etc
		packets = append(packets, Packet{
			IsServer: true,
			Data:     []string{"sv", strconv.Itoa(varIds[i]), strconv.Itoa(triggerNum)},
		})

		// TODO: test different orders? if they don't work then test for failure
		strVal := suitableValue(varOps[i], strconv.Itoa(varValues[i]))
		packets = append(packets, Packet{
			Data: []string{"sv", strconv.Itoa(varIds[i]), strVal},
		})
	}
	return packets
}

func sessionForCondition(c server.Condition, extra condExtra) Session {
	s := Session{}
	if c.Disabled {
		return s
	}

	values := c.Values
	if len(c.Values) == 0 {
		values = []string{c.Value}
	}
	if c.Trigger == "prevMap" {
		s.Packets = append(s.Packets, Packet{
			IsSession: true, Data: []string{"ploc", values[0], ""},
		})
		// TODO: test the rest of the values etc; same logic as with other multi-value triggers
	}

	if c.Map != 0 {
		s.RoomID = c.Map
	}
	if c.Trigger == "coords" || c.MapX1 != 0 || c.MapY1 != 0 || c.MapX2 != 0 || c.MapY2 != 0 {
		x := 0
		y := 0
		if c.MapX1 >= 0 {
			x = c.MapX1
		}
		if c.MapY1 >= 0 {
			y = c.MapY1
		}
		m := "m"
		if c.Trigger == "teleport" {
			m = "tp"
		}
		s.Packets = append(s.Packets, Packet{
			Data: []string{m, strconv.Itoa(x), strconv.Itoa(y)},
		})
	}

	triggerNum := 0
	switch c.Trigger {
	case "eventAction":
		triggerNum = 1
		fallthrough
	case "event":
		s.Packets = append(s.Packets, Packet{
			IsServer: true,
			Data:     []string{"sev", values[0], strconv.Itoa(triggerNum)},
		})
		s.Packets = append(s.Packets, Packet{
			Data: []string{"sev", values[0], strconv.Itoa(triggerNum)},
		})
		// TODO: check the rest of the values? we should be able to trigger this if we interact with any of the specified events
	}
	if c.Trigger == "picture" {
		s.Packets = append(s.Packets, Packet{
			IsServer: true,
			Data:     []string{"sp", values[0]},
		})
		s.Packets = append(s.Packets, Packet{
			// I am *not* checking all the variations of this one
			Data: []string{"ap", "1", "0", "0", "0", "0", "0", "0", "0", "0", "0", "200", "200", "200", "200", "0", "0", values[0], "0", "0", "0", "0", "0", "0", "0", "0", "0", "1", "0", "0", "0", "0"},
		})
		// TODO: check the rest of the values? we should be able to trigger this if we show any of the specified pictures
	}

	triggerNum = 0
	switchIds := c.SwitchIds
	switchValues := c.SwitchValues
	if len(switchIds) == 0 && c.SwitchId != 0 {
		switchIds = []int{c.SwitchId}
		switchValues = []bool{c.SwitchValue}
	}
	var switchPackets []Packet

	varIds := c.VarIds
	varOps := c.VarOps
	varValues := c.VarValues
	if len(varIds) == 0 && c.VarId != 0 {
		varIds = []int{c.VarId}
		varOps = []string{c.VarOp}
		varValues = []int{c.VarValue}
	}
	var varPackets []Packet
	if len(switchIds) > 0 {
		switchPackets = packetsForSwitches(&c, switchIds, switchValues)
	}
	if len(varIds) > 0 {
		varPackets = packetsForVars(&c, varIds, varOps, varValues)
	}
	if c.VarTrigger {
		s.Packets = append(s.Packets, varPackets...)
		s.Packets = append(s.Packets, switchPackets...)
	} else {
		s.Packets = append(s.Packets, switchPackets...)
		s.Packets = append(s.Packets, varPackets...)
	}

	if c.TimeTrial && extra.TimeTrialSecs > 0 {
		s.Packets = append(s.Packets, packetsForTimeTrial(c.Map, extra.TimeTrialSecs)...)
	}

	s.Packets = append(s.Packets, Packet{IsServer: true, Data: []string{"b"}})
	return s
}

func packetsForTimeTrial(mapid, secs int) []Packet {
	var packets []Packet
	packets = append(packets, Packet{
		IsServer: true,
		Data:     []string{"ss", "1430", "0"},
	})
	packets = append(packets, Packet{
		Data: []string{"ss", "1430", "1"},
	})
	packets = append(packets, Packet{
		IsServer: true,
		Data:     []string{"sv", "88", "0"},
	})
	packets = append(packets, Packet{
		Data: []string{"sv", "88", strconv.Itoa(secs - 1)},
	})
	packets = append(packets, Packet{
		IsServer:  true,
		IsSession: true,
		Data:      []string{"ttr", strconv.Itoa(mapid), strconv.Itoa(secs - 1)},
	})
	return packets
}

type condExtra struct {
	TimeTrialSecs int
}

func runCondSession(cond server.Condition, extra condExtra) {
	s := sessionForCondition(cond, extra)
	r := server.RoomById(s.RoomID)
	c := server.NewRoomClient(nil, server.NewSessionClient())
	msgs := make(chan []string, 128)
	go func() {
		for {
			sp, err := c.RecvMsg()
			if err != nil {
				panic(err)
			}
			msgs <- sp
		}
	}()
	smsgs := make(chan []string, 128)
	go func() {
		for {
			sp, err := c.RecvSessionMsg()
			if err != nil {
				panic(err)
			}
			smsgs <- sp
		}
	}()
	c.JoinRoom(r)

	log.Printf("session: %#v", s)

	firstSp := <-msgs
	log.Println("\tgot initial packet", firstSp)

	for _, p := range s.Packets {
		if p.IsServer {
			mch := msgs
			sn := "room"
			if p.IsSession {
				mch = smsgs
				sn = "sess"
			}
		servLoop:
			for {
				select {
				case sp := <-mch:
					log.Printf("\tgot %s %#v", sn, sp)
					if slices.Equal(sp, p.Data) {
						break servLoop
					} else {
						continue servLoop
					}
				case <-time.After(time.Second * 5):
					log.Fatal("timed out while waiting for server packet ", p.Data)
				}
			}

		} else if p.IsSession {
			c.SendSessionMsg(p.Data)
			log.Printf("\tsess send %#v", p)
		} else {
			log.Printf("\troom send %#v", p)
			c.SendMsg(p.Data)
		}
	}
}

func loadBadges(baseDir string) (games []string, badges [][]server.Badge, conds []map[string]server.Condition, err error) {
	badgeGameDents, err := os.ReadDir(filepath.Join(baseDir, "badges", "data"))
	if err != nil {
		return nil, nil, nil, err
	}

	for _, d := range badgeGameDents {
		if !d.IsDir() {
			continue
		}
		games = append(games, d.Name())
	}
	slices.Sort(games)

	badges = make([][]server.Badge, len(games))
	conds = make([]map[string]server.Condition, len(games))
	for gi, game := range games {
		badgeDir := filepath.Join(baseDir, "badges", "data", game)
		badgeDents, err := os.ReadDir(badgeDir)
		if err != nil {
			return nil, nil, nil, err
		}
		badgeNames := map[*server.Badge]string{}
		for _, d := range badgeDents {
			if d.IsDir() {
				continue
			}
			bname := d.Name()
			if !strings.HasSuffix(bname, ".json") {
				continue
			}

			data, err := os.ReadFile(filepath.Join(badgeDir, bname))
			if err != nil {
				return nil, nil, nil, fmt.Errorf("%s: %w", bname, err)
			}
			var b server.Badge
			if err := json.Unmarshal(data, &b); err != nil {
				return nil, nil, nil, fmt.Errorf("%s: %w", bname, err)
			}
			badges[gi] = append(badges[gi], b)
			badgeNames[&b] = bname
		}
		// for consistent test execution order
		slices.SortFunc(badges[gi], func(a, b server.Badge) int {
			return strings.Compare(badgeNames[&a], badgeNames[&b])
		})

		condDir := filepath.Join(baseDir, "badges", "conditions", game)
		condDents, err := os.ReadDir(condDir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// game has no conditions
				continue
			}
			return nil, nil, nil, err
		}
		conds[gi] = make(map[string]server.Condition)
		for _, d := range condDents {
			if d.IsDir() {
				continue
			}
			cname := d.Name()
			if !strings.HasSuffix(cname, ".json") {
				continue
			}

			data, err := os.ReadFile(filepath.Join(condDir, cname))
			if err != nil {
				return nil, nil, nil, fmt.Errorf("%s: %w", cname, err)
			}
			var c server.Condition
			if err := json.Unmarshal(data, &c); err != nil {
				return nil, nil, nil, fmt.Errorf("%s: %w", cname, err)
			}
			server.ConditionSetup(&c, cname)
			conds[gi][c.ConditionId] = c
		}
	}
	return games, badges, conds, nil
}

func main() {
	games, badges, conds, err := loadBadges(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}

	allRooms := make([]int, 0, 9999)
	for id := range 9999 {
		allRooms = append(allRooms, id)
	}
	server.TestInit(allRooms)
	server.LoadBadgeData(os.Args[1])

	// TODO: add function for single-condition mode like the one before this commit, it is still useful
	for gi, game := range games {
		server.SetGameName(game)
		for _, badge := range badges[gi] {
			var tags []string
			if badge.ReqString != "" {
				tags = append(tags, badge.ReqString)
			}
			if len(badge.ReqStrings) > 0 {
				tags = append(tags, badge.ReqStrings...)
			}
			for _, rsa := range badge.ReqStringArrays {
				tags = append(tags, rsa...)
			}

			var extra condExtra
			if badge.ReqType == "timeTrial" {
				extra.TimeTrialSecs = badge.ReqInt

				// time trial conds are not directly referenced in the badge file
				for cid, cond := range conds[gi] {
					if cond.Map == badge.Map && cond.TimeTrial {
						tags = append(tags, cid)
					}
				}
			}
			// TODO: minigame scores

			for _, cid := range tags {
				cond := conds[gi][cid]
				log.Println(cid)
				runCondSession(cond, extra)
			}
		}
	}
}
