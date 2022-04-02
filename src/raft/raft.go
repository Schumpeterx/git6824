package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	//	"bytes"
	"bytes"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	//	"6.824/labgob"
	"6.824/labgob"
	"6.824/labrpc"
)

const (
	FOLLOWER  = "FOLLOWER"
	CANDIDATE = "CANDIDATE"
	LEADER    = "LEADER"
	HEARTBEAT = 100 // send heartbeat per 100 ms
)

// return a random time duration
func randomTime() time.Duration {
	rand.Seed(time.Now().UnixNano())
	r := 2*HEARTBEAT + rand.Intn(50)
	return time.Duration(r) * time.Millisecond
}
func trySendChan(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}
func min(x int, y int) int {
	if x > y {
		return y
	}
	return x
}

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in part 2D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// For 2D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	// persist
	currentTerm       int
	votedFor          int
	log               []Log
	lastIncludedIndex int
	lastIncludedTerm  int
	lastApplied       int

	// volatile
	commitIndex    int
	nextIndex      []int
	matchIndex     []int
	state          string
	heartbeatTimer *time.Timer
	electTimer     *time.Timer
	electTimeout   time.Duration
	aeChan         []chan struct{}
	commitChan     chan struct{}
	applyChan      chan ApplyMsg
}
type Log struct {
	Command interface{}
	Term    int
}

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int
	VoteGranted bool
	SendTerm    int // 发送请求时，Candidate的term
}
type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []Log
	LeaderCommit int
}
type AppendEntriesReply struct {
	SendTerm int
	Term     int
	Success  bool

	ConflictTerm  int
	ConflictIndex int
}

// require lock
func (rf *Raft) printState() {
	DPrintf("Server[%v]: state=%v, term=%v, commit=%v, lastApplied=%v, lastIncludedIndex=%v,log_len=%v, lastCommand=%v", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.lastIncludedIndex, len(rf.log), rf.log[len(rf.log)-1].Command)
}

// require lock, transfer logic index into real index
func (rf *Raft) realIndex(logicIndex int) int {
	return logicIndex - rf.lastIncludedIndex
}

// require lock, transfer real index into logic index
func (rf *Raft) logicIndex(realIndex int) int {
	return realIndex + rf.lastIncludedIndex
}

// require lock, return logic index
func (rf *Raft) getLastLogIndex() int {
	return rf.logicIndex(len(rf.log) - 1)
}

// require lock, return logic index
func (rf *Raft) getFirstLogIndex() int {
	return rf.logicIndex(1)
}

// require lock
func (rf *Raft) toLeader() {
	DPrintf("\033[1;31;40mServer[%v] state change, from %v to Leader\033[0m", rf.me, rf.state)
	defer rf.printState()
	rf.state = LEADER
	// start heartbeat
	go rf.broadcastAppendEntries(true)
	// rf.heartbeatTimer.Stop()
	rf.heartbeatTimer.Reset(HEARTBEAT * time.Millisecond)
}

// require lock
func (rf *Raft) toFollower(term int, votedFor int) {
	DPrintf("\033[1;31;40mServer[%v] state change, from %v to Follower\033[0m", rf.me, rf.state)
	defer rf.printState()
	rf.state = FOLLOWER
	rf.currentTerm = term
	rf.votedFor = votedFor
	rf.persist()
	// ....
}

// require lock
func (rf *Raft) toCandidate() {
	DPrintf("\033[1;31;40mServer[%v] state change, from %v to Candidate\033[0m", rf.me, rf.state)
	defer rf.printState()
	rf.state = CANDIDATE
	rf.currentTerm++
	rf.votedFor = rf.me
	rf.electTimeout = randomTime()
	// rf.electTimer.Stop()
	rf.electTimer.Reset(rf.electTimeout)
	// start elect
	go rf.elect()
	rf.persist()
}

// require lock ,compare last log for vote
// is rf log is more recently, return true, else false
func (rf *Raft) isFreshThan(LastLogIndex int, lastLogTerm int) bool {
	if lastLogTerm > rf.log[len(rf.log)-1].Term || lastLogTerm == rf.log[len(rf.log)-1].Term && LastLogIndex >= rf.getLastLogIndex() {
		return false
	}
	return true
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	var term int
	var isleader bool
	// Your code here (2A).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	term = rf.currentTerm
	if rf.state == LEADER {
		isleader = true
	}
	return term, isleader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(rf.currentTerm)
	e.Encode(rf.votedFor)
	e.Encode(rf.lastApplied)
	e.Encode(rf.lastIncludedIndex)
	e.Encode(rf.lastIncludedTerm)
	e.Encode(rf.log)
	data := w.Bytes()
	rf.persister.SaveRaftState(data)
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var currentTerm, votedFor, lastApplied, lastIncludedIndex, lastIncludedTerm int
	var log []Log
	if d.Decode(&currentTerm) != nil ||
		d.Decode(&votedFor) != nil ||
		d.Decode(&lastApplied) != nil ||
		d.Decode(&lastIncludedIndex) != nil ||
		d.Decode(&lastIncludedTerm) != nil ||
		d.Decode(&log) != nil {
		//   error...
		DPrintf("\033[5;47;31mServer[%d] recover ERROR!!!: data=%+v\033[0m", rf.me, data)
	} else {
		rf.currentTerm = currentTerm
		rf.votedFor = votedFor
		rf.lastApplied = lastApplied
		rf.lastIncludedIndex = lastIncludedIndex
		rf.lastIncludedTerm = lastIncludedTerm
		rf.log = log
		DPrintf("\033[5;47;30mServer[%d] recover: %+v\033[0m", rf.me, rf)
	}
}

//
// A service wants to switch to snapshot.  Only do so if Raft hasn't
// have more recent info since it communicate the snapshot on applyCh.
//
func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {

	// Your code here (2D).

	return true
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).

}

// start an election, if win, switch to leader
func (rf *Raft) elect() {
	DPrintf("Server[%v] start a elect", rf.me)
	cnt := 1
	for i := 0; i < len(rf.peers); i++ {
		if i == rf.me {
			continue
		}
		rf.mu.Lock()
		args := &RequestVoteArgs{
			Term:         rf.currentTerm,
			CandidateId:  rf.me,
			LastLogIndex: rf.getLastLogIndex(),
			LastLogTerm:  rf.log[len(rf.log)-1].Term,
		}
		reply := &RequestVoteReply{}
		rf.mu.Unlock()
		go func(reply *RequestVoteReply, server int) {
			ok := rf.sendRequestVote(server, args, reply)
			if !ok {
				return
			}
			DPrintf("Server[%v] receive vote result from Server[%v], args={%+v}, reply={%+v}", rf.me, server, args, reply)
			rf.mu.Lock()
			defer rf.mu.Unlock()
			if rf.currentTerm < reply.Term {
				rf.toFollower(reply.Term, -1)
				return
			}
			if rf.state != CANDIDATE || reply.SendTerm != rf.currentTerm || !reply.VoteGranted {
				return
			}
			cnt++
			if cnt >= len(rf.peers)/2+1 {
				rf.toLeader()
			}
		}(reply, i)
	}
}

// deal vote request
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	DPrintf("Server[%d] receive vote request from Server[%d], args={%+v}, reply=%+v", rf.me, args.CandidateId, args, reply)
	if rf.currentTerm < args.Term {
		rf.toFollower(args.Term, -1)
	}
	reply.Term = rf.currentTerm
	if rf.currentTerm > args.Term || rf.currentTerm == args.Term && rf.votedFor != -1 && rf.votedFor != args.CandidateId {
		return
	}
	if rf.isFreshThan(args.LastLogIndex, args.LastLogTerm) {
		return
	}
	// prevent start a new election
	// rf.electTimer.Stop()
	rf.electTimer.Reset(rf.electTimeout)
	rf.votedFor = args.CandidateId
	reply.VoteGranted = true
	rf.persist()
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	DPrintf("Server[%v] send vote request to Server[%v], args=%+v, reply=%+v", rf.me, server, args, reply)
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	reply.SendTerm = args.Term
	return ok
}

func (rf *Raft) broadcastAppendEntries(isHeartbeat bool) {
	if isHeartbeat {
		// 2A
		rf.mu.Lock()
		args := &AppendEntriesArgs{
			Term:         rf.currentTerm,
			LeaderId:     rf.me,
			PrevLogIndex: rf.getLastLogIndex(),
			PrevLogTerm:  rf.log[len(rf.log)-1].Term,
			Entries:      nil,
			LeaderCommit: rf.commitIndex,
		}
		state := rf.state
		rf.mu.Unlock()
		if state != LEADER {
			return
		}
		for server := 0; server < len(rf.peers); server++ {
			if server == rf.me {
				continue
			}
			reply := &AppendEntriesReply{}
			go func(reply *AppendEntriesReply, server int) {
				ok := rf.sendAppendEntries(server, args, reply)
				if ok {
					rf.handleAppendEntriesReply(args, reply, server)
				} else {
					// if heartbeat lose, retry by using aeSender
					trySendChan(rf.aeChan[server])
				}
			}(reply, server)
		}
	} else {
		// 2B
		// ...
		for server := 0; server < len(rf.peers); server++ {
			if server != rf.me {
				go trySendChan(rf.aeChan[server])
			}
		}
	}
}

// wait for new append entries request, send and handle append entries requset
func (rf *Raft) aeSender(c chan struct{}, server int) {
	for !rf.killed() {
		<-c
		rf.mu.Lock()
		if rf.state != LEADER {
			rf.mu.Unlock()
			continue
		}
		prevLogIndex := rf.nextIndex[server] - 1
		index := rf.realIndex(prevLogIndex)
		prevLogTerm := rf.log[index].Term
		entries := make([]Log, len(rf.log[index+1:]))
		copy(entries, rf.log[index+1:])
		args := &AppendEntriesArgs{
			Term:         rf.currentTerm,
			LeaderId:     rf.me,
			PrevLogIndex: index,
			PrevLogTerm:  prevLogTerm,
			Entries:      entries,
			LeaderCommit: rf.commitIndex,
		}
		reply := &AppendEntriesReply{}
		rf.mu.Unlock()
		ok := rf.sendAppendEntries(server, args, reply)
		if !ok {
			trySendChan(c)
			continue
		}
		rf.handleAppendEntriesReply(args, reply, server)
	}
}
func (rf *Raft) handleAppendEntriesReply(args *AppendEntriesArgs, reply *AppendEntriesReply, server int) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if len(args.Entries) != 0 {
		DPrintf("Server[%v] receive AppendEntries reply form Server[%v], args={%+v}, reply={%+v}", rf.me, server, args, reply)
		rf.printState()
	}
	if rf.currentTerm < reply.Term {
		rf.toFollower(reply.Term, -1)
	}
	if rf.state != LEADER || reply.SendTerm != rf.currentTerm {
		return
	}
	// 2B
	// ...
	if !reply.Success {
		if reply.ConflictTerm == -1 {
			rf.nextIndex[server] = reply.ConflictIndex
		} else {
			i := len(rf.log) - 1
			for ; i > 0; i-- {
				if rf.log[i].Term == reply.ConflictTerm {
					break
				}
			}
			if i == 0 || rf.log[i].Term != reply.ConflictTerm {
				rf.nextIndex[server] = reply.ConflictIndex
			} else {
				rf.nextIndex[server] = rf.logicIndex(i + 1)
			}
		}
		trySendChan(rf.aeChan[server])
	} else {
		// update match index and next index and commit index
		rf.nextIndex[server] = rf.logicIndex(len(rf.log))
		oldMatchIndex := rf.matchIndex[server]
		if rf.matchIndex[server] < args.PrevLogIndex+len(args.Entries) {
			rf.matchIndex[server] = args.PrevLogIndex + len(args.Entries)
		}
		if rf.matchIndex[server]+1 != rf.nextIndex[server] {
			trySendChan(rf.aeChan[server])
		}
		if oldMatchIndex == rf.matchIndex[server] {
			return
		}
		// update commit index
		cnt := 1
		newCommit := rf.matchIndex[server]
		if rf.commitIndex < newCommit && rf.currentTerm == reply.Term {
			for s := 0; s < len(rf.peers); s++ {
				if s != rf.me {
					if rf.matchIndex[s] >= newCommit {
						cnt++
					}
				}
			}
			if cnt >= len(rf.peers)/2+1 {
				rf.commitIndex = newCommit
				trySendChan(rf.commitChan)
			}
		}
	}
}

// func (rf *Raft) handleHeartbeatReply(reply *AppendEntriesReply, server int) {
// 	rf.mu.Lock()
// 	defer rf.mu.Unlock()
// 	if rf.currentTerm < reply.Term {
// 		rf.toFollower(reply.Term, -1)
// 	}
// 	if rf.state != LEADER || reply.SendTerm != rf.currentTerm {
// 		return
// 	}
// 	// 2B
// 	// ...
// 	if !reply.Success{
// 		if reply.ConflictTerm == -1{
// 			rf.nextIndex[server] = reply.ConflictIndex
// 		}else{
// 			i :=len(rf.log)-1
// 			for ; i>0; i--{
// 				if rf.log[i].Term == reply.ConflictTerm{
// 					break
// 				}
// 			}
// 			if i==0 || rf.log[i].Term!=reply.ConflictTerm{
// 				rf.nextIndex[server] = reply.ConflictIndex
// 			}else{
// 				rf.nextIndex[server] = rf.logicIndex(i+1)
// 			}
// 		}
// 		trySendChan(rf.aeChan[server])
// 	}else{
// 		// match, but heartbeat haven't send any log, so dont update matchIndex
// 	}
// }
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if len(args.Entries) > 0 {
		defer DPrintf("Server[%d]\033[1;32;42m%+v\033[0m reply to append request %+v with reply %+v", rf.me, rf, args, reply)
		DPrintf("Server[%v] receive append entries request from Server[%v], args={%+v}", rf.me, args.LeaderId, args)
		rf.printState()
	}
	// DPrintf("Server[%v] receive append entries request from Server[%v], args={%+v}, reply=%+v", rf.me, args.LeaderId, args, reply)
	if rf.currentTerm < args.Term || rf.state == CANDIDATE && rf.currentTerm == args.Term {
		rf.toFollower(args.Term, -1)
	}
	reply.Term = rf.currentTerm
	if rf.currentTerm > args.Term {
		return
	}
	// prevent from starting an election
	// rf.electTimer.Stop()
	rf.electTimer.Reset(rf.electTimeout)
	// 2B
	// ...
	// check prevLogIndex and prevLogTerm
	if args.PrevLogIndex > rf.getLastLogIndex() {
		reply.ConflictIndex = rf.getLastLogIndex() + 1
		reply.ConflictTerm = -1
		return
	}
	pli := rf.realIndex(args.PrevLogIndex)
	if rf.log[pli].Term != args.PrevLogTerm {
		reply.ConflictTerm = rf.log[pli].Term
		i := pli
		for ; i > 0; i-- {
			if rf.log[i].Term != reply.ConflictTerm {
				break
			}
		}
		reply.ConflictIndex = rf.logicIndex(i + 1)
		return
	}
	reply.Success = true
	index := pli + 1
	end := rf.realIndex(args.PrevLogIndex + len(args.Entries))
	newLog := false
	for ; index < len(rf.log) && index <= end; index++ {
		if rf.log[index].Term != args.Entries[index-pli-1].Term {
			newLog = true
			break
		}
	}
	if index == len(rf.log) {
		newLog = true
	}
	if newLog {
		rf.log = rf.log[:pli+1]
		rf.log = append(rf.log, args.Entries...)
		rf.persist()
	}
	// check commitIndex
	if args.LeaderCommit > rf.commitIndex {
		rf.commitIndex = min(args.LeaderCommit, args.PrevLogIndex+len(args.Entries))
		trySendChan(rf.commitChan)
	}
}
func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	if len(args.Entries) > 0 {
		DPrintf("Server[%v] send append entries request to Server[%v], args=%+v, reply=%+v", rf.me, server, args, reply)
	}
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	reply.SendTerm = args.Term
	return ok
}

// when has new commitIndex, try to update applied index, and sent applyMsg to Tester or Service
func (rf *Raft) updateApplied() {
	for !rf.killed() {
		<-rf.commitChan
		rf.mu.Lock()
		for rf.lastApplied < rf.commitIndex {
			rf.lastApplied++
			rf.persist()
			applyMsg := ApplyMsg{
				CommandValid: true,
				Command:      rf.log[rf.realIndex(rf.lastApplied)].Command,
				CommandIndex: rf.lastApplied,
			}
			rf.mu.Unlock()
			rf.applyChan <- applyMsg
			DPrintf("\033[1;35;45mServer[%v] apply Command{%+v}\033[0m", rf.me, applyMsg)
			rf.mu.Lock()
		}
		rf.mu.Unlock()
	}
}

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	term = rf.currentTerm
	if rf.state == LEADER {
		isLeader = true
		newLog := Log{
			Command: command,
			Term:    rf.currentTerm,
		}
		rf.log = append(rf.log, newLog)
		rf.persist()
		index = rf.logicIndex(len(rf.log) - 1)
		DPrintf("\033[1;33;43mServer[%v] receive new Log={%+v} at index %d, log_len=%d\033[0m", rf.me, newLog, index, len(rf.log))

		go rf.broadcastAppendEntries(false)
	}
	return index, term, isLeader
}

//
// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
//
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

// The ticker go routine starts a new election if this peer hasn't received
// heartsbeats recently.
func (rf *Raft) ticker() {
	for !rf.killed() {

		// Your code here to check if a leader election should
		// be started and to randomize sleeping time using
		// time.Sleep().
		select {
		case <-rf.heartbeatTimer.C:
			rf.mu.Lock()
			if rf.state == LEADER {
				go rf.broadcastAppendEntries(true)
				// rf.heartbeatTimer.Stop()
				rf.heartbeatTimer.Reset(HEARTBEAT * time.Millisecond)
			}
			rf.mu.Unlock()
		case <-rf.electTimer.C:
			rf.mu.Lock()
			if rf.state != LEADER {
				rf.toCandidate()
			}
			rf.mu.Unlock()
			// default:
		}
	}
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).
	rf.commitIndex = 0
	rf.lastApplied = 0
	rf.votedFor = -1
	rf.currentTerm = 0
	rf.state = FOLLOWER
	rf.lastIncludedIndex = 0
	rf.lastIncludedTerm = 0
	rf.log = make([]Log, 1)
	rf.nextIndex = make([]int, len(rf.peers))
	rf.matchIndex = make([]int, len(rf.peers))
	rf.aeChan = make([]chan struct{}, len(rf.peers))
	rf.commitChan = make(chan struct{}, 1)
	rf.applyChan = applyCh
	for i := 0; i < len(rf.nextIndex); i++ {
		rf.nextIndex[i] = len(rf.log)
		rf.aeChan[i] = make(chan struct{}, 1)
		go rf.aeSender(rf.aeChan[i], i)
	}
	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())
	rf.log[0].Term = rf.lastIncludedTerm
	rf.electTimeout = randomTime()
	rf.heartbeatTimer = time.NewTimer(HEARTBEAT * time.Millisecond)
	rf.electTimer = time.NewTimer(rf.electTimeout)
	// start ticker goroutine to start elections
	go rf.ticker()
	go rf.updateApplied()
	return rf
}
