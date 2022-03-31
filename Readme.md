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