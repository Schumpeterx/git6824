# Lab 2
## Test时间
```
Test (2A): initial election ...
  ... Passed --   3.0  3   58   16474    0
Test (2A): election after network failure ...
  ... Passed --   4.4  3  142   26207    0
Test (2A): multiple elections ...
  ... Passed --   5.5  7  704  128429    0
Test (2B): basic agreement ...
  ... Passed --   0.6  3   16    4532    3
Test (2B): RPC byte count ...
  ... Passed --   1.3  3   48  114240   11
Test (2B): agreement despite follower disconnection ...
  ... Passed --   3.8  3   93   23651    7
Test (2B): no agreement if too many followers disconnect ...
  ... Passed --   3.4  5  251   48226    4
Test (2B): concurrent Start()s ...
  ... Passed --   0.6  3   16    4548    6
Test (2B): rejoin of partitioned leader ...
  ... Passed --   4.1  3  150   34287    4
Test (2B): leader backs up quickly over incorrect follower logs ...
  ... Passed --  16.6  5 1707 1079996  102
Test (2B): RPC counts aren't too high ...
  ... Passed --   2.0  3   42   12380   12
Test (2C): basic persistence ...
  ... Passed --   3.1  3   85   21429    6
Test (2C): more persistence ...
  ... Passed --  15.0  5 1119  221145   16
Test (2C): partitioned leader and one follower crash, leader restarts ...
  ... Passed --   1.4  3   40    9897    4
Test (2C): Figure 8 ...
  ... Passed --  37.5  5 2011  427558   77
Test (2C): unreliable agreement ...
  ... Passed --   2.4  5  517  162635  246
Test (2C): Figure 8 (unreliable) ...
  ... Passed --  44.4  5 4463 6558666  255
Test (2C): churn ...
  ... Passed --  16.5  5 2034 1748521  471
Test (2C): unreliable churn ...
  ... Passed --  16.5  5 1531  659709  243
Test (2D): snapshots basic ...
  ... Passed --   3.5  3  225   77316  251
Test (2D): install snapshots (disconnect) ...
  ... Passed --  50.8  3 1318  360380  388
Test (2D): install snapshots (disconnect+unreliable) ...
  ... Passed --  53.3  3 1568  395643  388
Test (2D): install snapshots (crash) ...
  ... Passed --  32.0  3  826  229836  388
Test (2D): install snapshots (unreliable+crash) ...
  ... Passed --  35.9  3  964  253609  366
PASS
ok      6.824/raft      357.927s
go test -race  67.75s user 9.75s system 21% cpu 5:58.29 total
```
## 总体设计
### 选举
选举超时计时器过期后，发起选举。
收到未过期的AE包，或者给其他节点投票后，重置选举计时器。
### 日志复制
Leader最多同时给某一个Follower发送一个AE调用。不能并行发送。这样能节约资源。当新的log到来，Leader更新自己的log，然后给所有Follower广播更新。如果某一个Follower的上一个AE包还没有返回，或者没有处理完毕，那么，针对他的更新就会被延迟，极端情况下，会有多个更新被延迟。但是，这是可以允许的，因为当上一个AE包处理完毕，或者超时后，发送下一个AE包时，可以将多个更新同时发送，这样，减少了AE包的发送次数。同时，将AE包强制串行化，降低了开发和调试难度。
Leader给每个Follower维护一个发送和处理AE包的协程和一个chan，当有新的log时，就通过chan通知AE协程。这个协程一次只能处理一个AE包，也就是说，下一个AE包（不包括心跳）必须等待这个AE包处理完毕才能发送。为了防止网络延迟导致Follower发起选举，比如，AE包的返回由于网络延迟，下一个AE包无法按时发送，导致选举发生。所以，心跳包必须单独处理。

### 持久化
不需要持久化lastApplied，重启或者修改lastIncludedIndex时，将lastApplied修改为lastIncludedIndex。因为当状态机重启时，需要raft重新apply，让状态机恢复到正常状态。
lastIncludedIndex需要持久化。这样，即使压缩日志，重启后也会从正确的地方开始。
### 日志压缩
把lastIncludedIndex和term都保存起来，需要持久化。
log的编号为逻辑上的编号，log的下标为log的真实下标，对应的转换关系为：
`逻辑编号 = 真实下标 + lastIncludedIndex`
日志的最后一条log的逻辑编号
`lastIncludedIndex + len(log) - 1`
日志的第一条log的逻辑编号
`lastIncludedIndex + 1`
## 关于bug
实验中产生bug很大一部分都是因为没有遵守论文或者guide的要求，所以，论文和guide需要熟读，需要尊重论文的设计，不能随意简化，细节非常重要。还有一部分是因为加锁后提前返回忘记释放锁导致死锁，最好是在加锁后跟上`defer rf.mu.Unlock`来释放锁。
发生bug基本都是由于代码编写有问题。有些bug需要测很多次才会出现，一两次通过test不能代表代码没有问题，不要心存侥幸，对于这种bug，最好是多次测试，打log来记录。
## 关于Debug
在写实验时，打log查bug是唯一的方式。最好把每次Test的log都保存到文件中，方便debug。
util.go中提供了DPrintf工具来打log，可以进一步修改，使他更符合需求。
```go
// test_test.go
var logfile *string
var logFiles *os.File

func TestMain(m *testing.M) {
	setup()
	ret := m.Run()
	teardown()
	os.Exit(ret)
}
func setup() {
	logfile = flag.String("log", "log/test.log", "Log file name")
	logFile, logErr := os.OpenFile(*logfile, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	logFiles = logFile
	if logErr != nil {
		fmt.Println("Fail to find", *logFile, "test start Failed")
		os.Exit(1)
	}
	multiWriter := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(multiWriter)
	log.SetFlags(log.Ldate | log.Ltime)
	//write log
	// log.Println("Test strat!")
}
func teardown() {
	logFiles.Close()
}
```
上面的代码修改test_test.go文件。在每次测试开始时，设置log打印的方式。我这里是把log保存到log/test.log中，同时显示在终端上。
同时，在输出日志时，还可以添加更多细节，比如颜色来凸显重要的日志。
```go
	DPrintf("\033[1;31;40mServer[%v] state change, from %v to Leader\033[0m", rf.me, rf.state)
```
比如这一句，通过\033[1;31;40m 和 \033[0m来调整日志的颜色。详细可参考链接：https://zhuanlan.zhihu.com/p/76751728
如果想打印结构体，将`%v`改成`%+v`就可以了。
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
4. 收到AE包，检查Term是否大于等于自己，是，则自己转为Follower，停止选举。
### Leader
1. Candidate晋升Leader后，马上开启心跳广播
2. 接收心跳广播的返回，如果返回term大于自己的term，说明有新的leader，转变成follower。
3. 如果心跳RPC失败，为了防止选举，要重新发送心跳
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
## Lab 2C
主要任务是持久化，每次持久化状态改变时，都要进行持久化。由于不能重复给Tester提供同一条Command，所以lastApplied也需要持久化。同时，2D中的lastInculudedIndex和Term都需要持久化。
需要持久化的字段如下：
1. currentTerm
2. votedFor
3. log[]
4. lastApplied
5. lastIncludedIndex and lastIncludedTerm

测试code
`for i in {0..10}; do go test -run 2C -race; done`
还可以同时开多个终端一起测试
### BUG
persist()和readPersist():两个函数中，变量的保存和读取顺序必须是一样的，否则读取会出现错误。

## Lab 2D
日志压缩。 每个Server相对独立的建立快照snapshot，也就是删除lastIncludedIndex及之前的log。当某个server远落后于leader的时候，leader还要将自己的快照发给这个server，让它跟上来。
在关于user和raft的交互中，论文和实验说明中的方法有一点区别。论文中，follower在接到leader的snapshot后，检查自己的log中是否有比snapshot更新的log，有，则需要保留这些新log，删除snapshot中已经存在的log。在实验说明中，follower在接到leader的snapshot之后，首先需要把这个snapshot发送到user，user再发送回raft，然后raft判断自己的log中有没有apply比这个snapshot更新的log，如果有，则丢弃这个snapshot，如果没有，则承认它。论文和实验说明的区别在于，论文中follower有可能需要向user发送一个新的snapshot，而实验中只需发送leader给的snapshot就行。
Raft在丢弃log时，注意要采用copy的方式进行，这样，go可以对丢弃的log空间进行回收。
raft通过persister.snapshot来获取snapshot
### User 和 Raft的交互
在Lab 2D中，user和raft有两种交互的接口：
1. `Snapshot(index int, snapshot []byte)`
    它由user主动触发。user从applyChan收到9个command后，调用一次Snapshot，通知raft进行snapshot。
2. `CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool`
    发送快照给raft，raft判断根据这个快照能不能删除自己的log，如果可以，则删除log，应用快照，并返回true。否则返回false；当返回true后，user就知道这个快照可以应用。
### Raft之间的交互
Leader通过`InstallSnapshot`发送RPC调用给follower。follower判断是否需要删除日志。这里，仍然用`aeSender`，也就是负责发送AE的协程，来发送`InstallSnapshot`，因为`InstallSnapshot`和`AppendEntries`只需要同时发送其中一种。
### Leader
1. 发现某个server的 nextIndex <= lastIncludedIndex， 表示要发给这个server的log，已经有一部分被删除了(小于等于lastIncludedIndex的部分)，这时候需要发送快照snapshot给server。
### Follower
1. follower接到snapshot后，首先要比较term，如果snapshot的term旧，直接返回。然后通过applyChan发送给user。再等待user调用`CondInstallSnapshot`返回这个snapshot，然后再判断是否有比snapshot最后一个log更新的log，如果没有，就应用这个snapshot。并持久化。返回true，通知user应用这个snapshot。
# Lab 3
基于Raft建立一个分布式的kv数据库。它支持线性化的get、put和append操作。
交互方式：
1. user也就是client通过clerk将请求发送给server（leader），并等待server返回结果。
2. server通过调用对应raft的start将请求交给raft
3. raft将请求log发送给majority后，apply请求到server
4. 此时server就可以执行请求包含的指令。比如get、put等。
5. server返回请求结果
6. 当raft apply的log超过阈值后，server通知raft进行snapshot
## Lab 3A
不包含snapshot功能。
### 第一部分
实现可靠情况下的基本功能。没有network and server failures。而且只有一个client。
1. client首先需要找到raft的leader是谁。
2. 发送命令，等待leader apply。
3. 发送回应给client。

### 第二部分
有network and server failures，以及多个client。这时候会出现许多问题
1. client重复发送request：如果一个请求失败，client会重复发送直到请求成功。但是，如果raft已经接收到请求，并且已经commit，只是返回时失败了，这时候再次接到请求，就会错误地重复commit这个请求。
2. server在收到请求，没有commit之前，从leader变成follower，这时候client需要重新寻找leader并重新发送请求。此时可能出现的问题在于，在失败的leader上的请求可能会收到别的leader在同一个index上的apply，但这并不是它等待的apply。解决方法是，根据客户的id+请求id+apply index作为通知请求的标识符。
#### 重复请求
一个请求已经commit，这时候client再次发送这个请求，就会导致重复请求的情况。
一个client一次只会发送一个请求，所以，在上一个请求没有成功返回之前，下一个请求不会发生。那么，对于重复的请求，有两种可能：
1. 这个重复请求在这之前都没有成功返回过，有可能是因为返回时失败，也有可能请求没有被commit。这时，没有下一个请求发生
2. 这个请求已经被成功返回过了，只不过由于网络问题，server又收到了这个请求。这时，下一个请求可能已经发生了
如果client都给每一个独立请求都赋一个递增的id，server就可以通过这个id来判断请求是不是过期的。server记录下每一个client的**已经commit**的最大请求id，以及这个id的应答，如果收到一个id小的请求，那这个请求肯定是由于网络延时导致重发的，可以忽略。如果又收到相同id为最大id的请求，那么重新发送应答。对于不同client的请求，他们之间的顺序是不会影响的。
新的问题是，如果leader更换了，新的leader如何得知每个client的最大请求id以及相应的应答。有一种情况会导致错误：在旧的leader提交了请求，但是由于旧leader网络partion，应答失败了。然后切换到新的leader，这个leader在收到重复请求后，就会重新提交请求。
可以通过将请求的id保存到raft中解决这个问题。当旧leader提交请求后，其他raft上的server也会从raft上获取到这个请求，然后应用到自己的状态机上。当新leader出现后，收到重复请求就能过分辨出来。此外，当server重启后，raft重新apply它的log，那么server又能重新获取到请求提交的信息，防止提交重复请求。