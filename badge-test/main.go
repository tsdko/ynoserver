package main

import (
	//"encoding/json"
	//"path/filepath"
	"log"
	"os"
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

func sessionForCondition(c server.Condition) Session {
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

	s.Packets = append(s.Packets, Packet{IsServer: true, Data: []string{"b"}})
	return s
}

func runCondSession(cond server.Condition) {
	s := sessionForCondition(cond)
	r := server.RoomById(s.RoomID)
	c := server.NewRoomClient(nil)
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
	c.JoinRoom(r)

	log.Printf("session: %#v", s)

	firstSp := <-msgs
	log.Println("\tgot initial packet", firstSp)

	for _, p := range s.Packets {
		if p.IsServer {
		servLoop:
			for {
				select {
				case sp := <-msgs:
					log.Printf("\tgot %#v", sp)
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

func main() {
	server.TestInit()
	allRooms := make([]int, 0, 9999)
	for id := range 9999 {
		allRooms = append(allRooms, id)
	}
	// TODO: test with all time trial thresholds loaded
	server.SetGameName("2kki")
	server.LoadBadgeData(os.Args[1], allRooms)
	// for osme reason we're not getting the right ocntitions right now
	// TODO: loop over all games? make sure to reset data between iterations (room and global conditions especially)
	// TODO: add function for single-condition mode like the one before this commit, it is still useful
	// (also I'm not even sure if non-isolated tests are gonna be non-jank enough to work as the "main" way of regression testing)

	for game, gameBadges := range server.Badges() {
		if game != "2kki" {
			continue
		}
		conds := server.Conditions()[game]
		server.SetGameName(game)
		// TODO: for time trial conditions get times from badge data
		//for _, badges := range gameBadges {
		// TODO: if we want to reference conds from rooms (why?) we'd need globalConditions access as well
		for cid, cond := range conds {
			if game == "2kki" && (cid == "abandoned_factory_garden" || strings.HasPrefix(cid, "tt_") || strings.HasSuffix(cid, "_tt") || strings.HasSuffix(cid, "_trial")) {
				log.Println("FIXME: skipping unsuported time trial condition", cid)
				continue
			}
			if game == "2kki" && cid == "jester_mask_fake_ap" {
				// missing map ID for some reason
				// FIXME: this *does* trigger in the proper server despite the lack of map id
				// it did apparently trigger in the single-condition mode as well
				cond.Map = gameBadges["jester_mask_apartments"].Map
			}
			log.Println(cid)
			runCondSession(*cond)
		}
	}
}
