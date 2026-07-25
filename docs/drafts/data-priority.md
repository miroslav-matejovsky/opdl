- I have very limited number of machines (master/server/integration)
- each machine has two instances (primary/standby)
- this means that it is easy to order the instances and generated data by priority, because the instances are already ordered by machine and role (primary/standby)
- lets assume each machine will distribute data to the other machines (each machine has data from master/slave/integration and knows if the data are provided by primary or standby)
- each machine can fail; network can fail; the data can be lost
- is this pattern known to achieve at least eventual consistency? (i.e. if the network is restored and the machines are restarted, will they eventually converge to the same state?)
- what to consider, be aware of, and avoid when implementing this
- maybe some ideas from crdts can be applied
- do research and give me some pointers on how to implement this pattern, and what to avoid

---

- leaderless / multi-master replication with anti-entropy repair, and the safe subset is state-based CRDT replication or operation-based CRDT replication.
- vector clocks
- epoch-number - Every time an instance crashes, restarts, or undergoes a state change, its epoch counter increases by exactly one (e → e + 1).
- Fencing Tokens: When an instance accesses shared resources (like a database or storage layer) after a restart, the storage node checks the epoch token. It rejects requests from any older instance epoch to prevent data corruption.
