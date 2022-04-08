package kvraft

import (
	"bytes"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
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

type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	Key   string
	Value string
	Op    string

	RequestId int
	UId       int64
	Err       Err
}
type Answer struct {
	Err   Err
	Value string
}
type KVServer struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
	persister     *raft.Persister
	m             map[string]string
	waitChan      map[string]chan Answer // key: uid + reqId +commandIndex
	lastRequestId map[int64]int
}

func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	// Your code here.
	op := Op{Key: args.Key, Op: GET, UId: args.UId, RequestId: args.RequestId}
	kv.DealRequest(&op)
	reply.Err = op.Err
	reply.Value = op.Value
}

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	kv.mu.Lock()
	// use map's zero value property
	if args.RequestId <= kv.lastRequestId[args.UId] {
		reply.Err = OK
		kv.mu.Unlock()
		return
	}
	kv.mu.Unlock()
	op := Op{Key: args.Key, Value: args.Value, Op: args.Op, UId: args.UId, RequestId: args.RequestId}
	kv.DealRequest(&op)
	reply.Err = op.Err
}
func (kv *KVServer) DealRequest(op *Op) {
	index, _, isLeader := kv.rf.Start(*op)
	DPrintf("Server[%d] deal request %+v", kv.me, op)
	if !isLeader {
		op.Err = ErrWrongLeader
		return
	}
	// wait for apply
	c := kv.getWaitChan(op.UId, op.RequestId, index)
	select {
	case answer := <-c:
		op.Value = answer.Value
		op.Err = answer.Err
	case <-time.After(WAIT_TIMEOUT * time.Second):
		op.Err = ErrWrongLeader
	}
	// delete wait chan
	go kv.deleteWaitChan(op.UId, op.RequestId, index)
}

// in case of wrong leader, use uid + reqId + index as the key of each waitChan
func (kv *KVServer) getWaitChan(uid int64, reqId, index int) chan Answer {
	key := fmt.Sprint(uid) + "," + fmt.Sprint(reqId) + "," + fmt.Sprint(index)
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if c, ok := kv.waitChan[key]; !ok {
		c = make(chan Answer, 1)
		kv.waitChan[key] = c
		return c
	} else {
		return c
	}
}
func (kv *KVServer) deleteWaitChan(uid int64, reqId, index int) {
	key := fmt.Sprint(uid) + "," + fmt.Sprint(reqId) + "," + fmt.Sprint(index)
	kv.mu.Lock()
	defer kv.mu.Unlock()
	delete(kv.waitChan, key)
}
func (kv *KVServer) createSnapshot() []byte {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(kv.lastRequestId)
	e.Encode(kv.m)
	data := w.Bytes()
	return data
}
func (kv *KVServer) readSnapshot(data []byte) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var lastRequestId map[int64]int
	var m map[string]string
	if d.Decode(&lastRequestId) != nil ||
		d.Decode(&m) != nil {
		//   error...
		DPrintf("\033[5;47;31mServer[%d] read ERROR!!!: data=%+v\033[0m", kv.me, data)
	} else {
		kv.m = m
		kv.lastRequestId = lastRequestId
		DPrintf("\033[5;47;30mServer[%d] read: %+v\033[0m", kv.me, data)
	}
}
func (kv *KVServer) fetchApply() {
	for !kv.killed() {
		command := <-kv.applyCh
		DPrintf("Server[%d] fetch apply command %+v", kv.me, command)
		if command.CommandValid {
			op := command.Command.(Op)
			index := command.CommandIndex
			answer := Answer{}
			kv.mu.Lock()
			if op.RequestId <= kv.lastRequestId[op.UId] && op.Op != GET {
				answer.Err = OK
			} else {
				if op.Op != GET {
					if op.Op == PUT {
						kv.m[op.Key] = op.Value
					} else if op.Op == APPEND {
						kv.m[op.Key] += op.Value
					}
					answer.Err = OK
				} else {
					if value, ok := kv.m[op.Key]; ok {
						answer.Value = value
						answer.Err = OK
					} else {
						answer.Err = ErrNoKey
					}
				}
			}
			if kv.lastRequestId[op.UId] < op.RequestId {
				kv.lastRequestId[op.UId] = op.RequestId
			}
			kv.mu.Unlock()
			_, isLeader := kv.rf.GetState()
			if isLeader {
				waitChan := kv.getWaitChan(op.UId, op.RequestId, index)
				DPrintf("Server[%d] send answer to %+v", kv.me, op)
				waitChan <- answer
			}
			// snapshot
			// save kv.m and kv.lastRequestId
			if kv.maxraftstate != -1 && kv.persister.RaftStateSize() >= kv.maxraftstate {
				kv.rf.Snapshot(index, kv.createSnapshot())
			}
		} else if command.SnapshotValid {
			// snapshot
			// call CondInstallSnapshot
			si := command.SnapshotIndex
			st := command.SnapshotTerm
			snapshot := command.Snapshot
			if kv.rf.CondInstallSnapshot(st, si, snapshot) {
				kv.readSnapshot(snapshot)
			}
		}
	}
}

//
// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
//
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
//
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate

	// You may need initialization code here.

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)
	kv.persister = persister
	// You may need initialization code here.
	kv.m = make(map[string]string)
	kv.lastRequestId = make(map[int64]int)
	kv.waitChan = make(map[string]chan Answer)
	kv.readSnapshot(kv.persister.ReadSnapshot())
	go kv.fetchApply()
	return kv
}
