package shardctrler

import (
	"fmt"
	"log"
	"sync"
	"time"

	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
)

const Debug = false
const WAIT_TIMEOUT = 1

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

type ShardCtrler struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg

	// Your data here.
	// killed bool
	// state machine of ShardCtrler
	configs []Config // indexed by config num
	// configId int      //configuration id

	waitChan map[string]chan Answer // key: uid + reqId +commandIndex
	// lastRequestId      map[int64]int
	lastRequestContext map[int64]RequestContext
}

type Op struct {
	// Your data here.
	Op string
	// JoinArgs
	Servers map[int][]string // new GID -> servers mappings
	// LeaveArgs
	GIDs []int
	// MoveArgs
	Shard int
	GID   int
	// QueryArgs
	Num int

	RequestId int
	UId       int64

	Err         Err
	WrongLeader bool
	Config      Config
}
type Answer struct {
	WrongLeader bool
	Err         Err
	Config      Config
}
type RequestContext struct {
	requestId int
	// op        Op
	answer Answer
}

// type Item struct {
// 	gid    int
// 	shards []int
// }
// type MinHeap []Item
// type MaxHeap []Item

// func (h MinHeap) Len() int      { return len(h) }
// func (h MaxHeap) Len() int      { return len(h) }
// func (h MinHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
// func (h MaxHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
// func (h MinHeap) Less(i, j int) bool {
// 	if len(h[i].shards) != len(h[j].shards) {
// 		return len(h[i].shards) < len(h[j].shards)
// 	} else {
// 		return h[i].gid < h[j].gid
// 	}
// }
// func (h MaxHeap) Less(i, j int) bool {
// 	if len(h[i].shards) != len(h[j].shards) {
// 		return len(h[i].shards) > len(h[j].shards)
// 	} else {
// 		return h[i].gid < h[j].gid
// 	}
// }
// func (h *MinHeap) Push(p interface{}) {
// 	*h = append(*h, p.(Item))
// }
// func (h *MaxHeap) Push(p interface{}) {
// 	*h = append(*h, p.(Item))
// }
// func (h *MinHeap) Pop() (x interface{}) {
// 	n := len(*h)
// 	x = (*h)[n-1]   // 返回删除的元素
// 	*h = (*h)[:n-1] // [n:m]不包括下标为m的元素
// 	return x
// }
// func (h *MaxHeap) Pop() (x interface{}) {
// 	n := len(*h)
// 	x = (*h)[n-1]   // 返回删除的元素
// 	*h = (*h)[:n-1] // [n:m]不包括下标为m的元素
// 	return x
// }

func (sc *ShardCtrler) getLastConfig() Config {
	return sc.configs[len(sc.configs)-1]
}
func (sc *ShardCtrler) Join(args *JoinArgs, reply *JoinReply) {
	// Your code here.
	sc.mu.Lock()
	if contexts, ok := sc.lastRequestContext[args.UId]; ok && contexts.requestId >= args.RequestId {
		reply.WrongLeader = false
		reply.Err = OK
		sc.mu.Unlock()
		return
	}
	sc.mu.Unlock()
	op := Op{Op: Join, Servers: args.Servers, UId: args.UId, RequestId: args.RequestId}
	sc.DealRequest(&op)
	reply.Err = op.Err
	reply.WrongLeader = op.WrongLeader
}

func (sc *ShardCtrler) Leave(args *LeaveArgs, reply *LeaveReply) {
	// Your code here.
	sc.mu.Lock()
	if contexts, ok := sc.lastRequestContext[args.UId]; ok && contexts.requestId >= args.RequestId {
		reply.WrongLeader = false
		reply.Err = OK
		sc.mu.Unlock()
		return
	}
	sc.mu.Unlock()
	op := Op{Op: Leave, GIDs: args.GIDs, UId: args.UId, RequestId: args.RequestId}
	sc.DealRequest(&op)
	reply.Err = op.Err
	reply.WrongLeader = op.WrongLeader
}

func (sc *ShardCtrler) Move(args *MoveArgs, reply *MoveReply) {
	// Your code here.
	sc.mu.Lock()
	if contexts, ok := sc.lastRequestContext[args.UId]; ok && contexts.requestId >= args.RequestId {
		reply.WrongLeader = false
		reply.Err = OK
		sc.mu.Unlock()
		return
	}
	sc.mu.Unlock()
	op := Op{Op: Move, GID: args.GID, Shard: args.Shard, UId: args.UId, RequestId: args.RequestId}
	sc.DealRequest(&op)
	reply.Err = op.Err
	reply.WrongLeader = op.WrongLeader
}

// only Query can repeat
func (sc *ShardCtrler) Query(args *QueryArgs, reply *QueryReply) {
	// Your code here.
	op := Op{Op: Query, Num: args.Num, UId: args.UId, RequestId: args.RequestId}
	sc.DealRequest(&op)
	reply.Err = op.Err
	reply.WrongLeader = op.WrongLeader
	reply.Config = op.Config
}
func (sc *ShardCtrler) DealRequest(op *Op) {
	index, _, isLeader := sc.rf.Start(*op)
	// if op.Op != Query {
	// DPrintf("Server[%d] deal request %+v", sc.me, op)
	// }
	if !isLeader {
		op.WrongLeader = true
		return
	}
	// wait for apply
	c := sc.getWaitChan(op.UId, op.RequestId, index)
	select {
	case answer := <-c:
		op.WrongLeader = answer.WrongLeader
		op.Err = answer.Err
		op.Config = answer.Config
	case <-time.After(WAIT_TIMEOUT * time.Second):
		op.WrongLeader = true
	}
	// delete wait chan
	go sc.deleteWaitChan(op.UId, op.RequestId, index)
}

// in case of wrong leader, use uid + reqId + index as the key of each waitChan
func (sc *ShardCtrler) getWaitChan(uid int64, reqId, index int) chan Answer {
	key := fmt.Sprint(uid) + "," + fmt.Sprint(reqId) + "," + fmt.Sprint(index)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if c, ok := sc.waitChan[key]; !ok {
		c = make(chan Answer, 1)
		sc.waitChan[key] = c
		return c
	} else {
		return c
	}
}
func (sc *ShardCtrler) deleteWaitChan(uid int64, reqId, index int) {
	key := fmt.Sprint(uid) + "," + fmt.Sprint(reqId) + "," + fmt.Sprint(index)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	delete(sc.waitChan, key)
}

// func (sc *ShardCtrler) dealJoin(newGroup map[int][]string) {
// 	lastConfig := sc.getLastConfig()
// 	groupNum := len(lastConfig.Groups) + len(newGroup)
// 	// average count of shard for each group
// 	cnt := NShards / groupNum
// 	// count each gid's shard
// 	// key: gid  value:[shard]
// 	gidShardMap := make(map[int][]int)
// 	groupList := make([]int, 0)
// 	newGroupMap := make(map[int][]int)
// 	newGroupList := make([]int, 0)
// 	// count each new or old group, and sort by gid
// 	for key, _ := range newGroup {
// 		newGroupMap[key] = make([]int, 0)
// 		newGroupList = append(newGroupList, key)
// 	}
// 	sort.Ints(newGroupList)
// 	for key, _ := range sc.getLastConfig().Groups {
// 		groupList = append(groupList, key)
// 	}
// 	sort.Ints(groupList)
// 	for shard, gid := range lastConfig.Shards {
// 		if list, ok := gidShardMap[gid]; ok {
// 			list = append(list, shard)
// 			gidShardMap[gid] = list
// 		} else {
// 			tmp := make([]int, 1)
// 			tmp[0] = shard
// 			gidShardMap[gid] = tmp
// 		}
// 	}
// 	for _, ngid := range newGroupList {
// 		for _, ogid := range groupList {
// 			if len(newGroupMap[ngid]) >= cnt {
// 				break
// 			}
// 			if len(gidShardMap[ogid]) >= cnt {
// 				tmp := newGroupMap[ngid]
// 				list := gidShardMap[ogid]
// 				start := len(gidShardMap[ogid]) - (cnt - len(newGroupMap[ngid]))
// 				// if len(gidShardMap[ogid])==cnt, it can only give out one more shard
// 				if start < cnt || len(gidShardMap[ogid]) == cnt{
// 					start = cnt
// 				}
// 				tmp = append(tmp, list[start:]...)
// 				list = list[:start]
// 				newGroupMap[ngid] = tmp
// 				gidShardMap[ogid] = list
// 			}
// 		}
// 	}
// 	config := Config{Num: lastConfig.Num + 1}
// 	for key, value := range newGroup {
// 		config.Groups[key] = value
// 	}
// 	for key, value := range lastConfig.Groups {
// 		config.Groups[key] = value
// 	}
// 	for gid, list := range gidShardMap {
// 		for _, shard := range list {
// 			config.Shards[shard] = gid
// 		}
// 	}
// 	for gid, list := range newGroupMap {
// 		for _, shard := range list {
// 			config.Shards[shard] = gid
// 		}
// 	}
// 	sc.configs = append(sc.configs, config)
// }
// func (sc ShardCtrler) dealLeave(GIDs []int) {
// 	lastConfig := sc.getLastConfig()
// 	groupNum := len(lastConfig.Groups) - len(GIDs)
// 	// average count of shard for each group
// 	cnt := NShards / groupNum
// 	// count each gid's shard
// 	// key: gid  value:[shard]
// 	gidShardMap := make(map[int][]int)
// 	// left group
// 	groupList := make([]int, 0)
// 	deleteGroupMap := make(map[int][]int)
// 	// count each new or old group, and sort by gid
// 	sort.Ints(GIDs)
// 	for shard, gid := range lastConfig.Shards {
// 		if list, ok := gidShardMap[gid]; ok {
// 			list = append(list, shard)
// 			gidShardMap[gid] = list
// 		} else {
// 			tmp := make([]int, 1)
// 			tmp[0] = shard
// 			gidShardMap[gid] = tmp
// 		}
// 	}
// 	for _,dgid := range GIDs{
// 		tmp := gidShardMap[dgid]
// 		sort.Ints(tmp)
// 		deleteGroupMap[dgid] = tmp
// 		delete(gidShardMap, dgid)
// 	}
// 	for gid,_ := range gidShardMap{
// 		groupList = append(groupList, gid)
// 	}
// 	sort.Ints(groupList)
// 	for _, dgid := range GIDs{
// 		for _, gid := range groupList{
// 			lenD := len(deleteGroupMap[dgid])
// 			lenO := len(gidShardMap[gid])
// 			if ledD==0{
// 				break
// 			}
// 			if lenO<=cnt{
// 				// if lenO==cnt, it only need one more shard
// 				list := deleteGroupMap[dgid]
// 				tmp := gidShardMap[gid]
// 				diff := cnt - lenO
// 				if diff > lenD{
// 					diff = lenD
// 				}

// 			}
// 		}
// 	}
// }
// func (sc *ShardCtrler) dealJoin(newGroup map[int][]string) {
// 	lastConfig := sc.getLastConfig()
// 	groupNum := len(lastConfig.Groups) + len(newGroup)
// 	// average count of shard for each group
// 	cnt := NShards / groupNum
// 	// count each gid's shard
// 	// key: gid  value:[shard]
// 	gidShardMap := make(map[int][]int)
// 	newGroupShardMap := make(map[int][]int)
// 	newGroupList := make([]int, 0)
// 	for gid, _ := range newGroup {
// 		newGroupList = append(newGroupList, gid)
// 	}
// 	sort.Ints(newGroupList)
// 	for shard, gid := range lastConfig.Shards {
// 		if list, ok := gidShardMap[gid]; ok {
// 			list = append(list, shard)
// 			gidShardMap[gid] = list
// 		} else {
// 			list := make([]int, 0)
// 			list = append(list, shard)
// 			gidShardMap[gid] = list
// 		}
// 	}
// 	// build a MaxHeap
// 	maxHeap := MaxHeap{}
// 	for gid, shards := range gidShardMap {
// 		newShards := make([]int, len(shards))
// 		copy(newShards, shards)
// 		maxHeap = append(maxHeap, Item{gid: gid, shards: newShards})
// 	}
// 	heap.Init(&maxHeap)
// 	// fetch a shard from heap top
// 	for _, ngid := range newGroupList {
// 		newGroupShardMap[ngid] = make([]int, 0)
// 		for len(newGroupShardMap[ngid]) < cnt {
// 			top := heap.Pop(&maxHeap).(Item)
// 			list := newGroupShardMap[ngid]
// 			if len(top.shards) > 1 {
// 				list = append(list, top.shards[0])
// 				top.shards = top.shards[1:]
// 			}
// 			heap.Push(&maxHeap, top)
// 			newGroupShardMap[ngid] = list
// 		}
// 	}
// 	config := Config{Num: lastConfig.Num + 1}
// 	for key, value := range newGroup {
// 		strs := make([]string, len(value))
// 		copy(strs, value)
// 		config.Groups[key] = strs
// 	}
// 	for key, value := range lastConfig.Groups {
// 		strs := make([]string, len(value))
// 		copy(strs, value)
// 		config.Groups[key] = strs
// 	}
// 	for gid, list := range gidShardMap {
// 		for _, shard := range list {
// 			config.Shards[shard] = gid
// 		}
// 	}
// 	for gid, list := range newGroupShardMap {
// 		for _, shard := range list {
// 			config.Shards[shard] = gid
// 		}
// 	}
// 	sc.configs = append(sc.configs, config)
// }

// func (sc *ShardCtrler) dealLeave(GIDs []int) {
// 	lastConfig := sc.getLastConfig()
// 	groupNum := len(lastConfig.Groups) - len(GIDs)
// 	cnt := NShards / groupNum
// 	sort.Ints(GIDs)
// 	leaveGroupShardMap := make(map[int][]int) // key=gid, value=shards
// 	leaveGroup := make(map[int][]string)
// 	remainGroup := make([]int, 0)
// 	remainGroupShardMap := make(map[int][]int)
// 	for _, gid := range GIDs {
// 		tmp := make([]string, len(lastConfig.Groups[gid]))
// 		copy(tmp, lastConfig.Groups[gid])
// 		leaveGroup[gid] = tmp
// 	}
// 	for shard, gid := range lastConfig.Shards {
// 		if _, ok := leaveGroup[gid]; ok {
// 			if _, ok := leaveGroupShardMap[gid]; !ok {
// 				leaveGroupShardMap[gid] = make([]int, 0)
// 			}
// 			list := leaveGroupShardMap[gid]
// 			list = append(list, shard)
// 			leaveGroupShardMap[gid] = list
// 		} else {
// 			if _, ok := remainGroupShardMap[gid]; !ok {
// 				remainGroupShardMap[gid] = make([]int, 0)
// 			}
// 			list := remainGroupShardMap[gid]
// 			list = append(list, shard)
// 			remainGroupShardMap[gid] = list
// 		}
// 	}
// 	for gid, _ := range lastConfig.Groups {
// 		if _, ok := leaveGroup[gid]; ok {
// 			continue
// 		}
// 		remainGroup = append(remainGroup, gid)
// 	}
// 	sort.Ints(remainGroup)
// 	minHeap := MinHeap{}
// 	for gid, shards := range remainGroupShardMap {
// 		newShards := make([]int, len(shards))
// 		copy(newShards, shards)
// 		minHeap = append(minHeap, Item{gid: gid, shards: newShards})
// 	}
// 	heap.Init(&minHeap)
// 	for _, lgid := range GIDs {
// 		list := leaveGroupShardMap[lgid]
// 		if len(list) < 1 {
// 			continue
// 		}
// 		top := heap.Pop(&minHeap).(Item)
// 		top.shards = append(top.shards, list[0])
// 		list = list[1:]
// 		leaveGroupShardMap[lgid] = list
// 		heap.Push(&minHeap, top)
// 		remainGroupShardMap[top.gid].append()
// 	}
// 	config := Config{}
// 	config.Num = lastConfig.Num+1
// 	groups := make(map[int][]string, 0)
// 	for _,gid := range remainGroup{
// 		for id, list := range lastConfig.Groups{
// 			l := make([]string, len(list))
// 			copy(l, list)
// 			if id==gid{
// 				groups[gid] = l
// 			}
// 		}
// 	}
// 	config.Groups = groups
// }
func (sc *ShardCtrler) dealJoin(servers map[int][]string) {
	lastConfig := sc.getLastConfig()
	config := sc.copyLastConfig()
	config.Num = lastConfig.Num + 1
	balance(&config.Shards, servers)
	for gid, strs := range servers {
		config.Groups[gid] = strs
	}
	sc.configs = append(sc.configs, config)
	// DPrintf("After Join: Configs:%+v", sc.configs)
}

// put all delete shards to gid zero, and then balance load
func (sc *ShardCtrler) dealLeave(GIDs []int) {
	lastConfig := sc.getLastConfig()
	config := sc.copyLastConfig()
	config.Num = lastConfig.Num + 1
	// gidShardMap := getShardsMap(config.Shards)
	// deleteShardMap := make(map[int][]int)
	// for _, gid := range GIDs {
	// 	list := gidShardMap[gid]
	// 	delete(gidShardMap, gid)
	// 	deleteShardMap[gid] = list
	// }
	// minGid := getGidWithMinShards(gidShardMap)
	// list := gidShardMap[minGid]
	// for _, tmp := range deleteShardMap {
	// 	list = append(list, tmp...)
	// }
	// sort.Ints(list)
	// gidShardMap[minGid] = list
	deleteGid := make(map[int]interface{})
	for _, gid := range GIDs {
		deleteGid[gid] = struct{}{}
		delete(config.Groups, gid)
	}
	for shard, gid := range config.Shards {
		if _, ok := deleteGid[gid]; ok {
			config.Shards[shard] = 0
		}
	}
	balance(&config.Shards, config.Groups)
	sc.configs = append(sc.configs, config)
}
func (sc *ShardCtrler) dealMove(Shard int, GID int) {
	lastConfig := sc.getLastConfig()
	config := sc.copyLastConfig()
	config.Num = lastConfig.Num + 1
	config.Shards[Shard] = GID
	sc.configs = append(sc.configs, config)
}
func (sc *ShardCtrler) dealQuery(Num int) Config {
	if Num == -1 || Num >= len(sc.configs) {
		config := sc.copyLastConfig()
		return config
	}
	config := sc.copyConfig(Num)
	return config
}

// if only left gid zero, return
// if gid zero has shards, give them to next gid, and then start balance load
func balance(shards *[NShards]int, newGroups map[int][]string) {
	m := getShardsMap(*shards)
	for gid := range newGroups {
		if _, ok := m[gid]; !ok {
			m[gid] = make([]int, 0)
		}
	}
	// DPrintf("Balance: map:%+v", m)
	if len(m) == 1 {
		return
	}
	next := getNext(m)
	if list, ok := m[0]; ok {
		tmp := m[next]
		for _, shard := range list {
			shards[shard] = next
		}
		tmp = append(tmp, list...)
		m[next] = tmp
		m[0] = make([]int, 0)
	}
	// DPrintf("After move zero gid to %v: map:%+v", next, m)
	for {
		min := getGidWithMinShards(m)
		max := getGidWithMaxShards(m)
		if len(m[min]) == len(m[max])-1 || len(m[max]) <= 1 || min == max {
			break
		}
		shard := m[max][0]
		m[min] = append(m[min], m[max][0])
		m[max] = m[max][1:]
		shards[shard] = min
	}
	// DPrintf("After Balance: shards:%+v", *shards)
}
func (sc *ShardCtrler) copyLastConfig() Config {
	lastConfig := sc.getLastConfig()
	config := Config{}
	config.Num = lastConfig.Num
	var shards [NShards]int
	for i := 0; i < NShards; i++ {
		shards[i] = lastConfig.Shards[i]
	}
	config.Shards = shards
	m := make(map[int][]string)
	for key, value := range lastConfig.Groups {
		copys := make([]string, len(value))
		copy(copys, value)
		m[key] = copys
	}
	config.Groups = m
	return config
}
func (sc *ShardCtrler) copyConfig(index int) Config {
	source := sc.configs[index]
	config := Config{}
	config.Num = source.Num
	var shards [NShards]int
	for i := 0; i < NShards; i++ {
		shards[i] = source.Shards[i]
	}
	config.Shards = shards
	m := make(map[int][]string)
	for key, value := range source.Groups {
		copys := make([]string, len(value))
		copy(copys, value)
		m[key] = copys
	}
	config.Groups = m
	return config
}

// return the min gid while gid > 0
func getNext(m map[int][]int) int {
	ans := -1
	for gid := range m {
		if gid == 0 {
			continue
		}
		if ans == -1 {
			ans = gid
		} else if gid < ans {
			ans = gid
		}
	}
	return ans
}

// find the least gid with max shards length
func getGidWithMaxShards(m map[int][]int) int {
	ma := -1
	var ans int
	for gid, list := range m {
		if gid == 0 {
			continue
		}
		if len(list) > ma {
			ma = len(list)
			ans = gid
		} else if len(list) == ma && gid < ans {
			ans = gid
		}
	}
	return ans
}

// find the least gid with min shards length
func getGidWithMinShards(m map[int][]int) int {
	mi := -1
	var ans int
	for gid, list := range m {
		if gid == 0 {
			continue
		}
		if mi == -1 {
			mi = len(list)
			ans = gid
		} else if len(list) < mi {
			mi = len(list)
			ans = gid
		} else if len(list) == mi && gid < ans {
			ans = gid
		}
	}
	return ans
}
func getShardsMap(shards [NShards]int) map[int][]int {
	m := make(map[int][]int)
	for shard, gid := range shards {
		if _, ok := m[gid]; !ok {
			m[gid] = make([]int, 0)
		}
		list := m[gid]
		list = append(list, shard)
		m[gid] = list
	}
	return m
}

// func min(a, b int) int {
// 	if a < b {
// 		return a
// 	}
// 	return b
// }
// func max(a, b int) int {
// 	if a < b {
// 		return b
// 	}
// 	return a
// }
func (sc *ShardCtrler) fetchApply() {
	for {
		command := <-sc.applyCh
		// DPrintf("Server[%d] fetch apply command %+v", sc.me, command)
		if command.CommandValid {
			op := command.Command.(Op)
			index := command.CommandIndex
			answer := Answer{}
			sc.mu.Lock()
			if contexts, ok := sc.lastRequestContext[op.UId]; ok && op.Op != Query && contexts.requestId >= op.RequestId {
				answer.WrongLeader = false
				answer.Err = OK
			} else {
				if op.Op != Query {
					if op.Op == Join {
						sc.dealJoin(op.Servers)
						answer.Err = OK
						answer.WrongLeader = false
					} else if op.Op == Leave {
						sc.dealLeave(op.GIDs)
						answer.Err = OK
						answer.WrongLeader = false
					} else if op.Op == Move {
						sc.dealMove(op.Shard, op.GID)
						answer.Err = OK
						answer.WrongLeader = false
					}
				} else {
					// DPrintf("Server[%d]: Configs:%+v, Context:%+v", sc.me, sc.configs, sc.lastRequestContext)
					config := sc.dealQuery(op.Num)
					answer.Err = OK
					answer.WrongLeader = false
					answer.Config = config
				}
			}
			if sc.lastRequestContext[op.UId].requestId < op.RequestId {
				context := RequestContext{requestId: op.RequestId, answer: answer}
				sc.lastRequestContext[op.UId] = context
			}
			sc.mu.Unlock()
			_, isLeader := sc.rf.GetState()
			if isLeader {
				waitChan := sc.getWaitChan(op.UId, op.RequestId, index)
				sc.mu.Lock()
				DPrintf("Server[%d] send answer:%+v \033[5;45;30mto\033[0m %+v", sc.me, answer, op)
				DPrintf("Server[%d] Configs: \033[5;47;30m%+v\033[0m, Context: \033[4;46;30m%+v\033[0m", sc.me, sc.configs, sc.lastRequestContext)
				sc.mu.Unlock()
				waitChan <- answer
			}
		}
	}
}

//
// the tester calls Kill() when a ShardCtrler instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (sc *ShardCtrler) Kill() {
	sc.rf.Kill()
	// Your code here, if desired.

}

// needed by shardsc tester
func (sc *ShardCtrler) Raft() *raft.Raft {
	return sc.rf
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant shardctrler service.
// me is the index of the current server in servers[].
//
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister) *ShardCtrler {
	sc := new(ShardCtrler)
	sc.me = me

	sc.configs = make([]Config, 1)
	sc.configs[0].Groups = map[int][]string{}

	labgob.Register(Op{})
	sc.applyCh = make(chan raft.ApplyMsg)
	sc.rf = raft.Make(servers, me, persister, sc.applyCh)

	// Your code here.
	// sc.lastRequestId = make(map[int64]int)
	sc.lastRequestContext = make(map[int64]RequestContext)
	sc.waitChan = make(map[string]chan Answer)

	go sc.fetchApply()
	return sc
}
