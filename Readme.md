# Lab 2
## 总体设计
### 选举
选举超时计时器过期后，发起选举。
收到未过期的AE包，或者给其他节点投票后，重置选举计时器。
### 日志复制
Leader最多同时给某一个Follower发送一个AE调用。不能并行发送。这样能节约资源。当新的log到来，Leader更新自己的log，然后给所有Follower广播更新。如果某一个Follower的上一个AE包还没有返回，或者没有处理完毕，那么，针对他的更新就会被延迟，极端情况下，会有多个更新被延迟。但是，这是可以允许的，因为当上一个AE包处理完毕，或者超时后，发送下一个AE包时，可以将多个更新同时发送，这样，减少了AE包的发送次数。同时，将AE包强制串行化，降低了开发和调试难度。
Leader给每个Follower维护一个发送和处理AE包的协程和一个chan，当有新的log时，就通过chan通知AE协程。这个协程一次只能处理一个AE包，也就是说，下一个AE包（不包括心跳）必须等待这个AE包处理完毕才能发送。为了防止网络延迟导致Follower发起选举，比如，AE包的返回由于网络延迟，下一个AE包无法按时发送，导致选举发生。所以，心跳包必须单独处理。

### 持久化
将lastApplied进行持久化，防止给Service发送重复的log
### 日志压缩
把lastIncludedIndex和term都保存起来，需要持久化。
log的编号为逻辑上的编号，log的下标为log的真实下标，对应的转换关系为：
`逻辑编号 = 真实下标 + lastIncludedIndex`
日志的最后一条log的逻辑编号
`lastIncludedIndex + len(log) - 1`
日志的第一条log的逻辑编号
`lastIncludedIndex + 1`
## Lab 2A
严格按照Figure 2来写。
### Follower
1. 检查currentTerm 和 投票请求的term，丢弃过期请求，拒绝投票直接返回。如果在当前term已经给其他peer投票，拒绝投票直接返回。 
2. 检查投票请求的log是否跟上自己。如果比自己旧，那么拒绝投票，返回。比较新旧的规则是：
    1. 先比较最后一个log的Term，term大的更新
    2. term相同的情况下，比较index的大小。也就是日志长度，日志越长越新
3. 转成follower，更新term，然后投票，重置选举超时。
4. 收到心跳包，检查term。 重置选举超时
<!-- 4. Lab 2B的任务：收到心跳，检查log是否与leader的log冲突，如果冲突，寻找冲突的Term出现的第一个下标，作为ConflictIndex。 如果log不包含leader的prevlog， 那么，ConflictIndex  -->
### Candidate
1. 选举超时，发起选举，转变成Candidate，给自己先投一票，然后广播投票
2. 收到投票结果，首先检查term是否大于自己。如果是，则更新自己的term，转成follower
3. 然后检查投票结果是否过期以及自己是不是还是Candidate，如果不是，丢弃结果。如果是，检查是不是已经获得大多数投票。是的话，就可以晋升Leader。
### Leader
1. Candidate晋升Leader后，马上开启心跳广播
2. 接收心跳广播的返回，如果返回term大于自己的term，说明有新的leader，转变成follower。
<!-- 3. 如果Success = false； 寻找log中term = Term的最大的下标，如果不存在，nextIndex = conflictIndex -->
## Lab 2B
主要关注日志复制、快速发送日志以及commitIndex和appliedIndex的更新
### Follower
收到AE请求后，进行相应的处理，然后返回结果
1. 对比term，检查是否需要更新term
2. 检查prevlogindex以及prevlogterm。 其中1和2项说明有冲突，直接返回false。并且1、2项在心跳包中也要进行检查。
    1. 如果在prevlogindex没有日志，那么，conflict index=len(log)，conflict term=-1. 
    2. 如果在prev log index有日志，但是term不匹配，conflict term = log[prevlogindex].term，然后，找到conflict term在log中第一次出现的下标，作为conflictindex返回。
    3. 如果在prev log index有日志，并且term匹配。那么，向后检查，request中的log是否与本地log匹配，如果完全匹配，说明这个请求是过期的。如果不完全匹配，重新进行匹配。success=true；
3. 检查请求中的commitIndex是不是大于自己的commitIndex，如果是，要更新commitIndex：
    `commitIndex = Math.min(leaderCommitIndex, new log Index)`。心跳包也要做此检查。
    
### Leader
为每个follower都维护一个协程，负责往对应的follower发送AE包，以及处理AE返回。
1. 每个协程监听一个对应的chan，chan的缓冲区大小为1。`[]chan interface{}`
2. 到协程对应的chan上有值时，说明有AE包需要发送，那么协程负责发送AE包以及处理请求
3. 处理完毕后，协程重新监听chan
4. 当有新的log，或者心跳检测返回false时，需要给follower发送AE包，这时候尝试往chan中放入一个信号，如果失败，说明chan中已经有信号了，无需阻塞等待。
5. 收到Ae包返回时，如果为false，在log中从后向前找到conflict term第一次出现的下标，再加1作为next index；如果没找到这样的下标，nextIndex = conflictIndex, 再次发送AE包； 如果为true，说明这个包成功append log到follower上，更新`matchIndex = prevLogIndex + len(Entries)； nextIndex = matchIndex + 1`,并且Leader只能提交当前Term的log。

### 更新lastApplied
每个peer都有一个协程专门负责更新lastApplied，以及将刚刚apply的log发送套applyChan中，上报给Tester或者Service，然后进行持久化(Lab 2C）； 每当peer的commit index改变时，通知该协程更新lastApplied。

### BUG
1. 在发送请求时，警告`labgob warning: Decoding into a non-default variable/field SendTerm may not work`。查询labgob源码发现：
    ``` go
                    // this warning typically arises if code re-uses the same RPC reply
                    // variable for multiple RPC calls, or if code restores persisted
                    // state into variable that already have non-default values.
                    fmt.Printf("labgob warning: Decoding into a non-default variable/field %v may not work\n",
                        what)
    ```
    意思是对多个RPC调用使用了同一个reply。打Log发现：
    ```
    2022/04/01 17:10:26 Server[2] aeSender send AppendEntries to Server[1], args={&{Term:1 LeaderId:2 PrevLogIndex:0 PrevLogTerm:0 Entries:[] LeaderCommit:0}}, reply=&{SendTerm:1 Term:0 Success:false ConflictTerm:0 ConflictIndex:0}

    2022/04/01 17:10:26 Server[1] receive append entries request from Server[2], args={&{Term:1 LeaderId:2 PrevLogIndex:0 PrevLogTerm:0 Entries:[] LeaderCommit:0}}, reply=&{SendTerm:0 Term:0 Success:false ConflictTerm:0 ConflictIndex:0}
    ```
    follower收到的reply的SendTerm变成了默认的0值。分析后发现在在RPC调用前，修改reply的值，RPC的接收方是看不到的。所以需要在RPC调用完成后，再修改reply中的SendTerm。
2. TestConcurrentStarts2B和TestCount2B都因为`test_test.go:601: term changed too often`错误而退出。刚开始还以为是日志发送速度太慢了。后来偶然发现是因为在`Start`函数里没有给`term`赋值导致。具体原因：
    ``` go
    _, term, ok := cfg.rafts[leader].Start(1)
    // ...
    if t, _ := cfg.rafts[j].GetState(); t != term {
        // term changed -- can't expect low RPC counts
        continue loop
    }
    ```
    第一行读取term，由于没有赋值，为0. 然后中间发送log给Leader，然后检查leader的term是否改变。这里，`GetState`能够正常取到Leader的term=1。导致重新开始循环，无法修改success参数，从而失败。