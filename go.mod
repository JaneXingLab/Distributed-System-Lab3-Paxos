module lab3

go 1.19

replace(
    "lab3/paxos" => ./paxos
    "lab3/kvpaxos" => ./kvpaxos
)