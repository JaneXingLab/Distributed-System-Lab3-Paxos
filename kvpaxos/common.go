package kvpaxos

import "hash/fnv"

const (
	OK       = "OK"
	ErrNoKey = "ErrNoKey"
)

type Err string

type PutArgs struct {
	Key       string
	Value     string
	DoHash    bool // For PutHash
	RequestID int
	ClientID  int
	// You'll have to add definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
}

type PutReply struct {
	Err           Err
	PreviousValue string // For PutHash
}

type GetArgs struct {
	Key       string
	RequestID int
	ClientID  int
	// You'll have to add definitions here.
}

type GetReply struct {
	Err   Err
	Value string
}

func hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}
