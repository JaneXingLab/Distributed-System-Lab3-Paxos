package paxos

//
// Paxos library, to be included in an application.
// Multiple applications will run, each including
// a Paxos peer.
//
// Manages a sequence of agreed-on values.
// The set of peers is fixed.
// Copes with network failures (partition, msg loss, &c).
// Does not store anything persistently, so cannot handle crash+restart.
//
// The application interface:
//
// px = paxos.Make(peers []string, me string)
// px.Start(seq int, v interface{}) -- start agreement on new instance
// px.Status(seq int) (decided bool, v interface{}) -- get info about an instance
// px.Done(seq int) -- ok to forget all instances <= seq
// px.Max() int -- highest instance seq known, or -1
// px.Min() int -- instances before this seq have been forgotten
//

import (
	"log"
	"math/rand"
	"net"
	"net/rpc"
	"os"
	"sync"
	"syscall"
	"time"
)

type Paxos struct {
	mu         sync.Mutex
	l          net.Listener
	dead       bool
	unreliable bool
	rpcCount   int
	peers      []string
	me         int // index into peers[]

	instances map[int]*Instance
	done      []int
	// Your data here.
}

type Instance struct {
	decided        bool
	n_p            int
	n_a            int
	max_p_rejected int
	v_a            interface{}
}

func makePaxos(peers []string, me int) *Paxos {
	px := &Paxos{}
	px.peers = peers
	px.me = me
	px.instances = make(map[int]*Instance)
	px.done = make([]int, len(peers))
	// initialize done array to -1 for all peers
	for i := range px.done {
		px.done[i] = -1 // 0 indicates that the peer has not called Done()
	}

	return px
}

// call() sends an RPC to the rpcname handler on server srv
// with arguments args, waits for the reply, and leaves the
// reply in reply. the reply argument should be a pointer
// to a reply structure.
//
// the return value is true if the server responded, and false
// if call() was not able to contact the server. in particular,
// the replys contents are only valid if call() returned true.
//
// you should assume that call() will time out and return an
// error after a while if it does not get a reply from the server.
//
// please use call() to send all RPCs, in client.go and server.go.
// please do not change this function.
func call(srv string, name string, args interface{}, reply interface{}) bool {
	c, err := rpc.Dial("unix", srv)
	if err != nil {
		//err1 := err.(*net.OpError)
		//if err1.Err != syscall.ENOENT && err1.Err != syscall.ECONNREFUSED {
		//  fmt.Printf("paxos Dial() failed: %v\n", err1)
		//}
		return false
	}
	defer c.Close()

	err = c.Call(name, args, reply)
	//fmt.Println(err)
	return err == nil
}

// the application wants paxos to start agreement on
// instance seq, with proposed value v.
// Start() returns right away; the application will
// call Status() to find out if/when agreement
// is reached.
func (px *Paxos) Start(seq int, v interface{}) {
	go px.startInstance(seq, v)
}

func (px *Paxos) startInstance(seq int, v interface{}) {
	// create a new instance if it doesn't exist
	px.mu.Lock()
	if _, exists := px.instances[seq]; !exists {
		px.instances[seq] = &Instance{
			decided:        false,
			n_p:            -1,  // no proposal yet
			n_a:            -1,  // no accepted proposal yet
			max_p_rejected: -1,  // no rejections yet
			v_a:            nil, // no value yet
		}
	}
	instance := px.instances[seq]
	px.mu.Unlock()
	for {
		px.mu.Lock()
		// check if the instance is already decided
		if instance.decided {
			// fmt.Printf("Instance %d is already decided with N_a=%d and V_a=%v\n", seq, px.instances[seq].n_a, px.instances[seq].v_a)
			px.mu.Unlock()
			break // no need to propose again
		}

		n_p := (instance.max_p_rejected/len(px.peers)+1)*len(px.peers) + px.me
		instance.n_p = n_p // update the proposal number

		countOk := 0

		max_na := instance.n_a // keep track of the maximum n_a seen
		max_va := instance.v_a // keep track of the value with maximum n_a
		px.mu.Unlock()

		// Phase 1: Prepare
		for index, peer := range px.peers {
			if px.dead {
				// fmt.Printf("Paxos is dead, aborting proposal for seq %d\n", seq)
				return
			}
			if peer == px.peers[px.me] {
				// fmt.Printf("Self %s prepared seq %d with N_p=%d\n", peer, seq, n_p)
				// skip self, we already proposed
				px.mu.Lock()
				countOk++
				if instance.n_a > max_na {
					max_na = instance.n_a
					max_va = instance.v_a
				}
				px.mu.Unlock()
				continue
			}
			// fmt.Printf("%d Sending Prepare to %s for seq %d with N_p=%d\n", px.me, peer, seq, n_p)
			px.mu.Lock()
			if instance.decided {
				// fmt.Printf("Paxos %d: Instance %d is already decided, breaking out of the loop\n", px.me, seq)
				px.mu.Unlock()
				break // exit early, no need to proceed
			}
			prepareArgs := &PrepareArgs{
				Seq: seq,
				N_p: n_p, // proposal number
			}
			px.mu.Unlock()
			prepareReply := &PrepareReply{}
			if call(peer, "Paxos.Prepare", prepareArgs, prepareReply) {
				// fmt.Printf("%d Received Prepare reply from %s for seq %d with Ok=%v, N_a=%d, V_a=%v\n", px.me, peer, seq, prepareReply.Ok, prepareReply.N_a, prepareReply.V_a)
				px.mu.Lock()
				px.done[index] = prepareReply.Done
				if prepareReply.Ok {
					// if the prepare was accepted
					countOk++
					if prepareReply.N_a > max_na {
						max_na = prepareReply.N_a
						max_va = prepareReply.V_a
					}
				} else {
					if prepareReply.N_p > instance.max_p_rejected {
						// update the highest rejection if we get a higher one
						instance.max_p_rejected = prepareReply.N_p
					}

					// if the instance is already decided, we should not proceed
					if prepareReply.Decided {
						// if the instance is already decided, we should not proceed
						instance.decided = true // mark as decided
						instance.n_a = prepareReply.N_a
						instance.v_a = prepareReply.V_a
						// fmt.Printf("Instance %d is already decided by %s with N_a=%d and V_a=%v\n", seq, peer, prepareReply.N_a, prepareReply.V_a)
						px.mu.Unlock()
						break // exit early, no need to proceed
					}
				}
				px.mu.Unlock()
			} else {
				// fmt.Printf("Failed to send Prepare to %s for seq %d\n", peer, seq)
			}
		}

		if instance.decided {
			// fmt.Printf("Paxos %d: Instance %d is already decided, breaking out of the loop\n", px.me, seq)
			break // no need to propose again
		}

		// Phase 2: Accept
		countOk2 := 0
		if countOk > len(px.peers)/2 {
			// we have majority, we can proceed to accept
			px.mu.Lock()
			if max_va == nil {
				instance.v_a = v // if no value was accepted, use the proposed value
			} else {
				instance.v_a = max_va // set the agreed value
			}
			px.mu.Unlock()
			for index, peer := range px.peers {
				if px.dead {
					// fmt.Printf("Paxos is dead, aborting proposal for seq %d\n", seq)
					return
				}
				px.mu.Lock()
				if peer == px.peers[px.me] {
					instance.n_a = instance.n_p // set the accepted proposal number
					countOk2++
					px.mu.Unlock()
					continue
				}
				// fmt.Printf("%d Sending Accept to %s for seq %d with N_p=%d and V_a=%v\n", px.me, peer, seq, instance.n_a, instance.v_a)
				acceptArgs := &AcceptArgs{
					Seq: seq,
					N_p: n_p,          // use the accepted proposal number
					V:   instance.v_a, // use the agreed value
				}
				px.mu.Unlock()
				acceptReply := &AcceptReply{}
				if call(peer, "Paxos.Accept", acceptArgs, acceptReply) {
					// fmt.Printf("%d Received Accept reply from %s for seq %d with Ok=%v, N_p=%d\n", px.me, peer, seq, acceptReply.Ok, acceptArgs.N_p)
					px.done[index] = acceptReply.Done
					if acceptReply.Ok {
						countOk2++
					} else {
						// fmt.Printf("Accept rejected by %s for seq %d with N_p=%d\n", peer, seq, acceptReply.N_p)
						px.mu.Lock()
						if acceptReply.N_p > instance.max_p_rejected {
							// update the highest rejection if we get a higher one
							instance.max_p_rejected = acceptReply.N_p
						}
						if acceptReply.V_a != nil {
							instance.v_a = acceptReply.V_a
							px.mu.Unlock()
							break
						}
						px.mu.Unlock()
					}
				} else {
					// fmt.Printf("Failed to send Accept to %s for seq %d\n", peer, seq)
				}
			}
		}
		if countOk2 > len(px.peers)/2 || instance.decided {
			break
		}
		time.Sleep(time.Duration(100) * time.Millisecond)
	}

	// Phase 3: Decide
	// notify the application that consensus has been reached
	for index, peer := range px.peers {
		if px.dead {
			// fmt.Printf("Paxos is dead, aborting proposal for seq %d\n", seq)
			return
		}
		if peer == px.peers[px.me] {
			// self does not need to call Decide, we already know the result
			// fmt.Printf("Paxos %d: Already decided for seq %d, skipping self\n", px.me, seq)
			instance.decided = true
			continue
		}
		// fmt.Printf("%d Sending Decide to %s for seq %d with V_a=%v\n", px.me, peer, seq, instance.v_a)
		px.mu.Lock()
		decideArgs := &DecideArgs{
			Seq: seq,
			V:   instance.v_a, // use the agreed value
		}
		px.mu.Unlock()
		decideReply := &DecideReply{}
		if call(peer, "Paxos.Decide", decideArgs, decideReply) {
			// fmt.Printf("%d Received Decide reply from %s for seq %d with Ok=%v\n", px.me, peer, seq, decideReply.Ok)
			px.done[index] = decideReply.Done // update the done array
			// update the done array
			// fmt.Printf("Decided for seq %d with V_a=%v on %s\n", seq, instance.v_a, peer)
		} else {
			// fmt.Printf("Failed to send Decide to %s for seq %d\n", peer, seq)
		}
	}
}

type PrepareArgs struct {
	Seq int
	N_p int // proposal number
}

type PrepareReply struct {
	Ok      bool
	Decided bool // whether the instance is already decided
	N_p     int
	N_a     int
	V_a     interface{}
	Done    int
}

func (px *Paxos) Prepare(args *PrepareArgs, reply *PrepareReply) error {
	px.mu.Lock()
	defer px.mu.Unlock()
	// fmt.Printf("%d Received Prepare for seq %d with N_p=%d\n", px.me, args.Seq, args.N_p)
	seq := args.Seq
	instance, exists := px.instances[seq]
	reply.Done = px.done[px.me] // calculate the minimum sequence number to forget
	if !exists {
		// create a new instance if it doesn't exist
		instance = &Instance{
			decided: false,
			n_p:     -1,  // no proposal yet
			n_a:     -1,  // no accepted proposal yet
			v_a:     nil, // no value yet
		}
		px.instances[seq] = instance
	}
	if px.instances[seq].decided {
		// if the instance is already decided, we should not accept any new proposals
		reply.Ok = false
		reply.Decided = true // indicate that the instance is already decided
		reply.N_a = instance.n_a
		reply.V_a = instance.v_a
		reply.N_p = instance.n_p // return the highest proposal number
		// fmt.Printf("%d : Instance %d is already decided with N_a=%d and V_a=%v\n", px.me, seq, instance.n_a, instance.v_a)
		return nil
	}
	// fmt.Printf("%d Received Prepare for seq %d with N_p=%d self n_p=%d\n", px.me, seq, args.N_p, instance.n_p)
	if args.N_p > instance.n_p {
		instance.n_p = args.N_p // update the proposal number
		reply.Ok = true
		reply.N_a = instance.n_a
		reply.V_a = instance.v_a
	} else {
		reply.Ok = false
		reply.N_p = instance.n_p // keep track of the highest proposal number
	}
	return nil

}

type AcceptArgs struct {
	Seq int
	N_p int         // proposal number
	V   interface{} // proposed value
}

type AcceptReply struct {
	N_p  int
	V_a  interface{}
	Ok   bool
	Done int
}

func (px *Paxos) Accept(args *AcceptArgs, reply *AcceptReply) error {
	px.mu.Lock()
	defer px.mu.Unlock()
	seq := args.Seq
	instance, exists := px.instances[seq]
	reply.Done = px.done[px.me] // calculate the minimum sequence number to forget
	if !exists {
		// create a new instance if it doesn't exist
		instance = &Instance{
			decided: false,
			n_p:     -1,  // no proposal yet
			n_a:     -1,  // no accepted proposal yet
			v_a:     nil, // no value yet
		}
		px.instances[seq] = instance
	}

	if args.N_p >= instance.n_p && !instance.decided {
		instance.n_p = args.N_p // update the proposal number
		instance.v_a = args.V   // set the proposed value
		instance.n_a = args.N_p // set the accepted proposal number
		reply.Ok = true
		// fmt.Printf("%d Accepted proposal for seq %d with N_p=%d and V_a=%v\n", px.me, seq, args.N_p, args.V)
	} else {
		reply.Ok = false
		reply.N_p = instance.n_p // keep track of the highest proposal number
		if instance.decided {
			reply.V_a = instance.v_a
		}
		// fmt.Printf("%d Rejecting proposal for seq %d with N_p=%d\n", px.me, seq, args.N_p)
	}
	return nil
}

type DecideArgs struct {
	Seq int
	V   interface{} // agreed value
}

type DecideReply struct {
	Done int
	Ok   bool
}

func (px *Paxos) Decide(args *DecideArgs, reply *DecideReply) error {
	px.mu.Lock()
	defer px.mu.Unlock()
	seq := args.Seq
	instance, exists := px.instances[seq]
	if !exists {
		// create a new instance if it doesn't exist
		instance = &Instance{
			decided: false,
			n_p:     -1,  // no proposal yet
			n_a:     -1,  // no accepted proposal yet
			v_a:     nil, // no value yet
		}
		px.instances[seq] = instance
	}

	// mark the instance as decided with the agreed value
	instance.decided = true
	// fmt.Printf("%d Deciding for seq %d with V=%v, decision: %t\n", px.me, seq, args.V, px.instances[seq].decided)
	instance.v_a = args.V // set the agreed value
	reply.Ok = true
	reply.Done = px.done[px.me] // calculate the minimum sequence number to forget
	// fmt.Printf("Decided for seq %d with V=%v\n", seq, args.V)
	return nil
}

// the application on this machine is done with
// all instances <= seq.
//
// see the comments for Min() for more explanation.
func (px *Paxos) Done(seq int) {
	px.mu.Lock()
	defer px.mu.Unlock()
	// fmt.Printf("Paxos %d: Done called with seq %d\n", px.me, seq)
	if px.done[px.me] < seq {
		px.done[px.me] = seq
	}
}

// the application wants to know the
// highest instance sequence known to
// this peer.
func (px *Paxos) Max() int {
	px.mu.Lock()
	defer px.mu.Unlock()
	maxSeq := -1
	for seq := range px.instances {
		if seq > maxSeq {
			maxSeq = seq // find the highest sequence number
		}
	}
	return maxSeq // return the highest instance sequence known
}

// Min() should return one more than the minimum among z_i,
// where z_i is the highest number ever passed
// to Done() on peer i. A peers z_i is -1 if it has
// never called Done().
//
// Paxos is required to have forgotten all information
// about any instances it knows that are < Min().
// The point is to free up memory in long-running
// Paxos-based servers.
//
// It is illegal to call Done(i) on a peer and
// then call Start(j) on that peer for any j <= i.
//
// Paxos peers need to exchange their highest Done()
// arguments in order to implement Min(). These
// exchanges can be piggybacked on ordinary Paxos
// agreement protocol messages, so it is OK if one
// peers Min does not reflect another Peers Done()
// until after the next instance is agreed to.
//
// The fact that Min() is defined as a minimum over
// *all* Paxos peers means that Min() cannot increase until
// all peers have been heard from. So if a peer is dead
// or unreachable, other peers Min()s will not increase
// even if all reachable peers call Done. The reason for
// this is that when the unreachable peer comes back to
// life, it will need to catch up on instances that it
// missed -- the other peers therefor cannot forget these
// instances.
func (px *Paxos) Min() int {
	px.mu.Lock()
	defer px.mu.Unlock()
	// fmt.Print("Calculating Min() for Paxos ", px.me, "\n")
	// fmt.Print(px.done, "\n")
	minSeq := px.done[px.me]
	for _, seq := range px.done {
		if seq < minSeq {
			minSeq = seq // find the minimum done sequence
		}
	}
	for seq := range px.instances {
		if seq <= minSeq {
			delete(px.instances, seq)
		}
	}
	// fmt.Printf("Paxos %d: Min() calculated as %d\n", px.me, minSeq+1)
	return minSeq + 1 // return one more than the minimum done sequence
}

// the application wants to know whether this
// peer thinks an instance has been decided,
// and if so what the agreed value is. Status()
// should just inspect the local peer state;
// it should not contact other Paxos peers.
func (px *Paxos) Status(seq int) (bool, interface{}) {
	px.mu.Lock()
	defer px.mu.Unlock()
	// fmt.Printf("Checking status for seq %d on Paxos %d\n", seq, px.me)
	instance, exist := px.instances[seq]
	// fmt.Printf("Checking status for seq %d on Paxos %d\n", seq, px.me)

	if !exist {
		// instance does not exist
		// fmt.Printf("Paxos %d: Status for seq %d: instance does not exist\n", px.me, seq)
		return false, nil
	}

	if instance.decided {
		// instance has been decided
		//fmt.Printf("Paxos %d : Status for seq %d: decided with N_a=%d and V_a=%v\n", px.me, seq, instance.n_a, instance.v_a)
		return true, instance.v_a // return the agreed value
	} else {
		// instance has not been decided
		//fmt.Printf("Paxos %d :Status for seq %d: not decided yet\n", px.me, seq)
		return false, nil // no agreed value
	}
}

// tell the peer to shut itself down.
// for testing.
// please do not change this function.
func (px *Paxos) Kill() {
	px.dead = true
	if px.l != nil {
		px.l.Close()
	}
}

// the application wants to create a paxos peer.
// the ports of all the paxos peers (including this one)
// are in peers[]. this servers port is peers[me].
func Make(peers []string, me int, rpcs *rpc.Server) *Paxos {
	px := makePaxos(peers, me)

	// Your initialization code here.

	if rpcs != nil {
		// caller will create socket &c
		rpcs.Register(px)
	} else {
		rpcs = rpc.NewServer()
		rpcs.Register(px)

		// prepare to receive connections from clients.
		// change "unix" to "tcp" to use over a network.
		os.Remove(peers[me]) // only needed for "unix"
		l, e := net.Listen("unix", peers[me])
		if e != nil {
			log.Fatal("listen error: ", e)
		}
		px.l = l

		// please do not change any of the following code,
		// or do anything to subvert it.

		// create a thread to accept RPC connections
		go func() {
			for !px.dead {
				conn, err := px.l.Accept()
				if err == nil && !px.dead {
					if px.unreliable && (rand.Int63()%1000) < 100 {
						// discard the request.
						conn.Close()
					} else if px.unreliable && (rand.Int63()%1000) < 200 {
						// process the request but force discard of reply.
						c1 := conn.(*net.UnixConn)
						f, _ := c1.File()
						err := syscall.Shutdown(int(f.Fd()), syscall.SHUT_WR)
						if err != nil {
							// fmt.Printf("shutdown: %v\n", err)
						}
						px.rpcCount++
						go rpcs.ServeConn(conn)
					} else {
						px.rpcCount++
						go rpcs.ServeConn(conn)
					}
				} else if err == nil {
					conn.Close()
				}
				if err != nil && !px.dead {
					// fmt.Printf("Paxos(%v) accept: %v\n", me, err.Error())
					// fmt.Print(px.done)
				}
			}
		}()
	}

	return px
}
