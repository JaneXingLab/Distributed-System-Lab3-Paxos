package kvpaxos

import (
	"fmt"
	"net/rpc"
	"time"
)

var clientID int = 0

type Clerk struct {
	servers      []string
	clientID     int
	nextRequstID int
	// You will have to modify this struct.
}

func MakeClerk(servers []string) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	ck.clientID = clientID
	clientID++
	ck.nextRequstID = 0
	// You'll have to add code here.
	return ck
}

// call() sends an RPC to the rpcname handler on server srv
// with arguments args, waits for the reply, and leaves the
// reply in reply. the reply argument should be a pointer
// to a reply structure.
//
// the return value is true if the server responded, and false
// if call() was not able to contact the server. in particular,
// the reply's contents are only valid if call() returned true.
//
// you should assume that call() will time out and return an
// error after a while if it doesn't get a reply from the server.
//
// please use call() to send all RPCs, in client.go and server.go.
// please don't change this function.
func call(srv string, rpcname string,
	args interface{}, reply interface{}) bool {
	c, errx := rpc.Dial("unix", srv)
	if errx != nil {
		return false
	}
	defer c.Close()

	err := c.Call(rpcname, args, reply)
	//fmt.Println(err)
	return err == nil
}

// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
func (ck *Clerk) Get(key string) string {
	// You will have to modify this function.
	fmt.Printf("Get, client: %d, requestID: %d, key: %s\n", ck.clientID, ck.nextRequstID, key)
	args := GetArgs{}
	args.Key = key
	args.ClientID = ck.clientID
	args.RequestID = ck.nextRequstID
	var reply GetReply
	for _, server := range ck.servers {
		go call(server, "KVPaxos.Get", &args, &reply)
		time.Sleep(50 * time.Millisecond)
	}
	for {
		if reply.Err == "OK" {
			return reply.Value
		}
		time.Sleep(10 * time.Millisecond) // short sleep before retryin
	}
}

// set the value for a key.
// keeps trying until it succeeds.
func (ck *Clerk) PutExt(key string, value string, dohash bool) string {
	// You will have to modify this function.
	fmt.Printf("Put, client: %d, requestID: %d, key: %s, value: %s\n", ck.clientID, ck.nextRequstID, key, value)
	args := PutArgs{}
	args.Key = key
	args.Value = value
	args.DoHash = dohash
	args.ClientID = ck.clientID
	args.RequestID = ck.nextRequstID
	var reply PutReply
	for _, server := range ck.servers {
		go call(server, "KVPaxos.Put", &args, &reply)
		time.Sleep(50 * time.Millisecond)
	}
	for {
		if reply.Err == "OK" {
			return reply.PreviousValue
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (ck *Clerk) Put(key string, value string) {
	ck.PutExt(key, value, false)
}
func (ck *Clerk) PutHash(key string, value string) string {
	v := ck.PutExt(key, value, true)
	return v
}
