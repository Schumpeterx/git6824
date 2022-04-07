package kvraft

import (
	"crypto/rand"
	"math/big"
	"sync"

	"6.824/labrpc"
)

type Clerk struct {
	servers []*labrpc.ClientEnd
	// You will have to modify this struct.
	uid       int64
	requestId int
	leaderId  int
	mu        sync.Mutex
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	ck.uid = nrand()
	ck.leaderId = 0
	ck.requestId = 0
	// You'll have to add code here.
	return ck
}

//
// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.Get", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
func (ck *Clerk) Get(key string) string {

	// You will have to modify this function.
	ck.mu.Lock()
	leader := ck.leaderId
	ck.requestId++
	ck.mu.Unlock()
	oldLeader := leader
	DPrintf("\033[0;42;30mClerk[%d] want get %s\033[0m", ck.uid, key)
	for {
		args := GetArgs{Key: key, UId: ck.uid, RequestId: ck.requestId}
		reply := GetReply{}
		ok := ck.servers[leader].Call("KVServer.Get", &args, &reply)
		if !ok || reply.Err == ErrWrongLeader {
			leader = (leader + 1) % len(ck.servers)
			continue
		}
		DPrintf("\033[0;42;30mClerk[%d] get %s success\033[0m", ck.uid, key)
		if oldLeader != leader {
			ck.mu.Lock()
			ck.leaderId = leader
			ck.mu.Unlock()
		}
		if reply.Err == ErrNoKey {
			return ""
		}
		return reply.Value
	}
}

//
// shared by Put and Append.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.PutAppend", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
func (ck *Clerk) PutAppend(key string, value string, op string) {
	// You will have to modify this function.
	ck.mu.Lock()
	leader := ck.leaderId
	ck.requestId++
	ck.mu.Unlock()
	oldLeader := leader
	DPrintf("\033[0;42;30mClerk[%d] want %s (%s,%s)\033[0m", ck.uid, op, key, value)
	for {
		args := PutAppendArgs{Key: key, Value: value, Op: op, UId: ck.uid, RequestId: ck.requestId}
		reply := PutAppendReply{}
		ok := ck.servers[leader].Call("KVServer.PutAppend", &args, &reply)
		if !ok || reply.Err != OK {
			leader = (leader + 1) % len(ck.servers)
			continue
		}
		if oldLeader != leader {
			ck.mu.Lock()
			ck.leaderId = leader
			ck.mu.Unlock()
		}
		DPrintf("\033[0;42;30mClerk[%d] %s (%s,%s) success\033[0m", ck.uid, op, key, value)
		return
	}
}

func (ck *Clerk) Put(key string, value string) {
	ck.PutAppend(key, value, "Put")
}
func (ck *Clerk) Append(key string, value string) {
	ck.PutAppend(key, value, "Append")
}
