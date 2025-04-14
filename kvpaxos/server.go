package kvpaxos

import (
	"net"
	"time"
)
import "fmt"
import "net/rpc"
import "log"
import "lab3/paxos"
import "sync"
import "os"
import "syscall"
import "encoding/gob"
import "math/rand"

const Debug = 0

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug > 0 {
		log.Printf(format, a...)
	}
	return
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
	done       map[int64]int

	// Your definitions here.
}

func makeKVPaxos(me int) *KVPaxos {
	kv := &KVPaxos{}
	kv.me = me
	kv.kvdatabase = make(map[string]string)
	// Your initialization code here.
	kv.nextSeq = 0
	kv.done = make(map[int64]int)
	return kv
}

func (kv *KVPaxos) Get(args *GetArgs, reply *GetReply) error {
	// Your code here.
	//done, exists := kv.done[args.ClientID]
	//if exists && done >= args.RequestID {
	//
	//}
	previousValue := kv.kvdatabase[args.Key]
	to := 10 * time.Millisecond
	for {
		op := Op{Optype: "Get", Key: args.Key, ClientID: args.ClientID, RequestID: args.RequestID}
		for {
			seq := kv.nextSeq
			fmt.Printf("Client: %d Get(%d): %s %s, Seq: %d\n", args.ClientID, kv.me, args.Key, kv.kvdatabase[args.Key], seq)
			decided, _ := kv.px.Status(seq)
			if decided {
				kv.mu.Lock()
				_, dOp := kv.px.Status(seq)
				op, ok := dOp.(Op)
				fmt.Printf("Decided: %t Value: %s Seq: %d, Op: %+v\n", decided, kv.kvdatabase[op.Key], seq, op)
				if ok {
					if op.RequestID != args.RequestID || op.ClientID != args.ClientID {
						fmt.Printf("Paxos: %d Not matching Seq: %d \n", kv.me, seq)
						missingOptype := op.Optype
						if missingOptype != "Get" {
							fmt.Printf("Paxos: %d Missing %+v\n", kv.me, op)
							if op.Optype == "PutHash" {

								fmt.Printf("Previous value: %s, new value: %s, Seq:%d \n", previousValue, op.Value, seq)
								kv.kvdatabase[op.Key] = previousValue + op.Value
							} else {
								fmt.Printf("Previous value: %s, new value: %s, Seq:%d \n", previousValue, op.Value, seq)
								kv.kvdatabase[op.Key] = op.Value
							}
							previousValue = kv.kvdatabase[op.Key]
						}
						kv.nextSeq++
						kv.mu.Unlock()
						break
					} else {
						fmt.Printf("Paxos: %d Matching Seq: %d, GetValue: %s Op: %+v\n", kv.me, seq, kv.kvdatabase[op.Key], op)
						reply.Value = kv.kvdatabase[op.Key]
						reply.Err = OK
						kv.nextSeq++
						kv.mu.Unlock()
						return nil
					}
				} else {
					fmt.Println("type assertion failed")
				}
				kv.mu.Unlock()
			} else {
				fmt.Printf("Paxos: %d start Seq: %d\n", kv.me, seq)
				kv.px.Start(seq, op)
				time.Sleep(to)
				if to < 100*time.Millisecond {
					to *= 2
				}
			}
		}
	}
}

func (kv *KVPaxos) Put(args *PutArgs, reply *PutReply) error {
	reply.PreviousValue = kv.kvdatabase[args.Key]
	to := 10 * time.Millisecond

	var opType string
	if args.DoHash {
		opType = "PutHash"
	} else {
		opType = "Put"
	}
	for {
		op := Op{Optype: opType, Key: args.Key, Value: args.Value, ClientID: args.ClientID, RequestID: args.RequestID}
		seq := kv.nextSeq
		fmt.Printf("Client: %d Put(%d): %s : %s, Seq: %d\n", args.ClientID, kv.me, args.Key, args.Value, seq)
		for {
			if seq < kv.nextSeq {
				break
			}
			decided, _ := kv.px.Status(seq)
			if decided {
				kv.mu.Lock()
				_, dOp := kv.px.Status(seq)
				op, ok := dOp.(Op)
				fmt.Printf("Decided: %t Seq: %d, Op: %+v\n", decided, seq, op)
				if ok {
					if op.RequestID != args.RequestID || op.ClientID != args.ClientID {
						fmt.Printf("Paxos: %d Not matching Seq: %d\n", kv.me, seq)
						missingOptype := op.Optype
						if missingOptype != "Get" {
							fmt.Printf("Missing %+v\n", op)
							if op.Optype == "PutHash" {
								kv.kvdatabase[op.Key] = reply.PreviousValue + op.Value
								fmt.Printf("Previous value: %s, new value: %s, Seq:%d \n", reply.PreviousValue, op.Value, seq)
							} else {
								fmt.Printf("Previous value: %s, new value: %s, Seq:%d \n", reply.PreviousValue, op.Value, seq)
								kv.kvdatabase[op.Key] = op.Value
							}
							reply.PreviousValue = kv.kvdatabase[op.Key]
						}
						kv.nextSeq++
						kv.mu.Unlock()
						break
					} else {
						reply.Err = OK
						if op.Optype == "PutHash" {
							kv.kvdatabase[op.Key] = reply.PreviousValue + op.Value
						} else {
							kv.kvdatabase[op.Key] = op.Value
						}
						fmt.Printf("Paxos: %d Matching Seq: %d new Value: %s, pre Value: %s, Op: %+v\n", kv.me, seq, op.Value, reply.PreviousValue, op)
						kv.nextSeq++
						kv.mu.Unlock()
						return nil
					}
				} else {
					fmt.Println("type assertion failed")
				}
				kv.mu.Unlock()
			} else {
				fmt.Printf("Paxos: %d start Seq: %d\n", kv.me, seq)
				kv.px.Start(seq, op)
				time.Sleep(to)
				if to < 100*time.Millisecond {
					to *= 2
				}
			}
		}
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
						fmt.Printf("shutdown: %v\n", err)
					}
					go rpcs.ServeConn(conn)
				} else {
					go rpcs.ServeConn(conn)
				}
			} else if err == nil {
				conn.Close()
			}
			if err != nil && kv.dead == false {
				fmt.Printf("KVPaxos(%v) accept: %v\n", me, err.Error())
				kv.kill()
			}
		}
	}()

	return kv
}
