package kvpaxos

import (
	"net"
	"time"
)
import "net/rpc"
import "log"
import "lab3/paxos"
import "sync"
import "os"
import "syscall"
import "encoding/gob"
import "math/rand"
import "strconv"

const Debug = 0

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug > 0 {
		log.Printf(format, a...)
	}
	return
}

type Entry struct {
	LastRequstID   int
	LastReplyValue string
}

type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	Optype    string
	Key       string
	Value     string
	ClientID  int
	RequestID int
}

type KVPaxos struct {
	mu         sync.Mutex
	l          net.Listener
	me         int
	dead       bool // for testing
	unreliable bool // for testing
	px         *paxos.Paxos
	kvdatabase map[string]string
	nextSeq    int
	done       map[int]Entry

	// Your definitions here.
}

func makeKVPaxos(me int) *KVPaxos {
	kv := &KVPaxos{}
	kv.me = me
	kv.kvdatabase = make(map[string]string)
	// Your initialization code here.
	kv.nextSeq = 0
	kv.done = make(map[int]Entry)
	return kv
}

func (kv *KVPaxos) isDuplicate(clientID int, requestID int) (bool, string) {
	lastRequst, ok := kv.done[clientID]
	return ok && lastRequst.LastRequstID >= requestID, kv.done[clientID].LastReplyValue
}

func (kv *KVPaxos) waitForDecision(seq int, op Op) {
	to := 10 * time.Millisecond
	go kv.px.Start(seq, op) //  Restart Paxos.
	for {
		decided, _ := kv.px.Status(seq)
		if decided {
			return
		}
		time.Sleep(to)
		if to < 10*time.Second {
			to *= 2
		}
	}
}

func (kv *KVPaxos) apply(op Op) string {
	prev := kv.kvdatabase[op.Key]
	if op.Optype == "Put" {
		kv.kvdatabase[op.Key] = op.Value
		//fmt.Printf("KVPaxos:%d applied Put Key: %s, Value: %s\n", kv.me, op.Key, op.Value)
	} else if op.Optype == "PutHash" {
		hashed := strconv.Itoa(int(hash(prev + op.Value)))
		kv.kvdatabase[op.Key] = hashed
	}
	kv.done[op.ClientID] = Entry{op.RequestID, prev}
	return prev
}

func (kv *KVPaxos) Get(args *GetArgs, reply *GetReply) error {
	// Your code here.
	seq := kv.nextSeq
	for {
		if seq < kv.nextSeq {
			seq = kv.nextSeq
		}
		op := Op{Optype: "Get", Key: args.Key, Value: kv.kvdatabase[args.Key], ClientID: args.ClientID, RequestID: args.RequestID}
		decided, _ := kv.px.Status(seq)
		if !decided {
			kv.waitForDecision(seq, op)
		}
		kv.mu.Lock()
		_, v := kv.px.Status(seq)
		//fmt.Printf("decided Seq: %d v: %+v, op: %+v\n", seq, v, op)
		op, ok := v.(Op)

		if ok {
			isDup, lastReply := kv.isDuplicate(op.ClientID, op.RequestID)
			if op.RequestID != args.RequestID || op.ClientID != args.ClientID {
				if !isDup {
					if op.Optype != "Get" {
						kv.apply(op)
					} else {
						kv.done[op.ClientID] = Entry{op.RequestID, op.Value}
					}
					kv.px.Done(seq)
				}
				//fmt.Printf("KV: %d has done Seq: %d, Op: %+v\n", kv.me, seq, op)
				seq++
			} else {
				if isDup {
					reply.Value = lastReply
					reply.Err = OK
					kv.mu.Unlock()
					//fmt.Printf("KV: %d has done Seq: %d, Op: %+v\n", kv.me, seq, op)
					kv.nextSeq = seq + 1
					//fmt.Printf("KV: %d, database: %+v\n", kv.me, kv.kvdatabase)
					return nil
				}
				reply.Value = op.Value
				reply.Err = OK
				//fmt.Printf("KV: %d has done Seq: %d, Op: %+v\n", kv.me, seq, op)
				kv.nextSeq = seq + 1
				kv.done[args.ClientID] = Entry{args.RequestID, kv.kvdatabase[args.Key]}
				//fmt.Printf("KV: %d, database: %+v\n", kv.me, kv.kvdatabase)
				kv.mu.Unlock()
				return nil
			}
		}
		kv.mu.Unlock()
	}
}

func (kv *KVPaxos) Put(args *PutArgs, reply *PutReply) error {
	var opType string
	if args.DoHash {
		opType = "PutHash"
	} else {
		opType = "Put"
	}
	seq := kv.nextSeq
	for {
		op := Op{Optype: opType, Key: args.Key, Value: args.Value, ClientID: args.ClientID, RequestID: args.RequestID}
		if seq < kv.nextSeq {
			seq = kv.nextSeq
		}

		decided, _ := kv.px.Status(seq)
		if !decided {
			kv.waitForDecision(seq, op)
		}
		kv.mu.Lock()
		_, v := kv.px.Status(seq)
		//fmt.Printf("decided Seq: %d v: %+v, op: %+v\n", seq, v, op)

		op, ok := v.(Op)
		if ok {
			isDup, lastReply := kv.isDuplicate(op.ClientID, op.RequestID)
			if op.RequestID != args.RequestID || op.ClientID != args.ClientID {
				if !isDup {
					if op.Optype != "Get" {
						kv.apply(op)
					} else {
						kv.done[op.ClientID] = Entry{op.RequestID, op.Value}
					}
				}
				seq++
			} else {
				if isDup {
					reply.PreviousValue = lastReply
					reply.Err = OK
					kv.nextSeq = seq + 1
					kv.mu.Unlock()
					return nil
				}
				reply.Err = OK
				reply.PreviousValue = kv.apply(op)
				kv.px.Done(seq)
				kv.nextSeq = seq + 1
				kv.mu.Unlock()
				return nil
			}
		}
		kv.mu.Unlock()
	}
}

// tell the server to shut itself down.
// please do not change this function.
func (kv *KVPaxos) kill() {
	DPrintf("Kill(%d): die\n", kv.me)
	kv.dead = true
	kv.l.Close()
	kv.px.Kill()
}

// servers[] contains the ports of the set of
// servers that will cooperate via Paxos to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
func StartServer(servers []string, me int) *KVPaxos {
	// this call is all that's needed to persuade
	// Go's RPC library to marshall/unmarshall
	// struct Op.
	gob.Register(Op{})
	kv := makeKVPaxos(me)

	rpcs := rpc.NewServer()
	rpcs.Register(kv)

	kv.px = paxos.Make(servers, me, rpcs)

	os.Remove(servers[me])
	l, e := net.Listen("unix", servers[me])
	if e != nil {
		log.Fatal("listen error: ", e)
	}
	kv.l = l

	// please do not change any of the following code,
	// or do anything to subvert it.

	go func() {
		for kv.dead == false {
			conn, err := kv.l.Accept()
			if err == nil && kv.dead == false {
				if kv.unreliable && (rand.Int63()%1000) < 100 {
					// discard the request.
					conn.Close()
				} else if kv.unreliable && (rand.Int63()%1000) < 200 {
					// process the request but force discard of reply.
					c1 := conn.(*net.UnixConn)
					f, _ := c1.File()
					err := syscall.Shutdown(int(f.Fd()), syscall.SHUT_WR)
					if err != nil {
					}
					go rpcs.ServeConn(conn)
				} else {
					go rpcs.ServeConn(conn)
				}
			} else if err == nil {
				conn.Close()
			}
			if err != nil && kv.dead == false {
				kv.kill()
			}
		}
	}()

	return kv
}
