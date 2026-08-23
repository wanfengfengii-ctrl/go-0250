# WindowProof 已安装门窗气密、水密与抗风压联检放行闭环

## 项目目标

WindowProof 是面向建筑工程质量实验室的单节点 Go 联检服务，管理一批已安装门窗单元从构造与材料锁定、试验资源独占、安装密封确认，到气密正负压分级、水密定时喷淋、抗风压变形回弹复核，再到双人独立复核和唯一放行、返修隔离或取消终局的完整闭环。实现以任务聚合为一致性边界，用一次性资源令牌、有序压力前缀、喷淋检查点覆盖集合、不可覆盖证据版本链和终局栅栏处理并发及迟到回执。SQLite WAL 持久化所有业务状态、幂等结果和审计事件，支持进程重启恢复。规模规划为 24-28 个生产 Go 文件、约 2500 行有效生产 Go 代码并控制在 2000-3000 行，分布于至少 7 个有明确职责的包；公开测试不少于 5 个文件和 16 个确定性用例。

## 端到端业务流程

1. 实验室依据楼栋立面分区选择已安装窗单元，核对朝向及型材、玻璃、密封材料批次，选定方案修订、压力序列、喷淋检查点、阈值、试验箱和位移测点，随后原子锁定任务快照与资源令牌。
2. 安装人员按锁定方向将试件连接试验箱，逐项确认边界封堵、接缝密封和测点归零；全部确认一致后，任务从试件安装中进入气密加载中。
3. 操作人员按冻结序列提交正负压级次。每一级先登记可重试仪器调用，再采集压力与空气流量，完成整数换算及阈值判定；只有当前级成功后才开放下一级。
4. 气密前缀完成后进入水密喷淋中。系统把喷嘴区段和时间片建模为检查点覆盖集合，在当前压力与锁定秒数内接收喷淋传感器回执和渗水观察证据。
5. 水密覆盖完成后进入抗风压复核中，按有序级次采集加载位移、卸载回弹和残余位移；测点结果经整数换算后与冻结阈值比较。
6. 发现渗水、密封失效或残余变形时建立证据版本链。返修决定递增任务代次，只重开受影响的级次和喷淋检查点，同时保持旧证据及旧回执不可变。修复无法继续时可进入返修隔离终局。缺陷全部闭合且试验义务完成后进入待独立复核。两名资质合格且身份不同的人员分别确认同一摘要，任务转为可放行。放行、返修隔离或取消通过同一终局仲裁器竞争，成功放行生成稳定凭据并释放全部占用。

## 核心组件与职责

1. 门窗构造与材料规则目录：建议 internal/catalog，约 4 个生产 Go 文件，保存固定测试目录和版本化规则，计算规范化 SHA-256 锁定摘要。
2. 联检任务聚合：建议 internal/inspection，约 5 个文件，定义状态机、命令校验、任务代次、操作幂等摘要与领域事件。
3. 试件/试验箱/测点占用账簿：建议 internal/occupancy，约 3 个文件，以数据库唯一约束和条件更新实现多资源全取或全拒绝。
4. 分级压力与喷淋采集账簿：建议 internal/acquisition，约 5 个文件，实现加载前缀、覆盖集合、脚本化仪器调用、整数安全运算和结果判定。
5. 缺陷证据及复核终局仲裁器：建议 internal/verdict，约 4 个文件，实现证据版本链、返修重开范围、双人复核、放行摘要及终局竞争。
6. Go HTTP API 与真实质检操作页面：建议 internal/store、internal/api、web 和 cmd/windowproof，共约 7 个生产 Go 文件；SQLite WAL 负责事务持久化和启动恢复，页面由后端嵌入并提供实际操作表单。

## 领域规则与不变量

1. 任务锁定使用目录修订的规范化快照；窗单元必须属于锁定立面分区，朝向及三类材料批次必须与该窗单元规则完全匹配。锁定后目录更新不追溯修改任务。
2. 状态转换仅由聚合根执行。安装确认之后才能加载；气密、水密、抗风压必须依次完成；待独立复核之前不得写入复核；终态没有出边。
3. 压力级次按 phase、polarity、ordinal 组成固定顺序。允许同操作号同内容重放，禁止不同操作号重复写入已完成级次，也禁止提交当前游标之后的级次。
4. 安全整数运算集中在一个纯函数模块：所有输入先验证非负范围，乘法在执行前检查 MaxInt64/因子，除法明确拒绝零分母，比例换算采用半向远离零舍入。
5. 喷淋覆盖以冻结检查点 ID 集合为目标。只有压力容差、持续秒数、喷嘴状态和传感器格式同时有效时才能加入覆盖集合；集合写入与状态推进属于同一事务。
6. 渗水、密封失效和残余变形证据只追加不修改。闭合记录必须引用当前代次证据及修复验证结果；新代次不能删除旧代次内容。
7. 每个外部调用先持久化 attempt，再由可控适配器执行。拒绝、断连、超时和格式错误保留为失败状态；重试创建新的 attempt，并引用原操作。
8. 复核人员必须在锁定资质修订中有效，且两次复核身份不同、结论摘要相同。生成放行凭据与设置已放行、释放资源在一个事务中完成。
9. 所有拒绝按稳定错误码返回 Reasons 数组；数组固定按窗单元编号、压力级次、测点编号、检查点编号和错误码排序。失败事务回滚领域记录、游标、覆盖、占用和结论。
10. 并发写操作使用 SQLite BEGIN IMMEDIATE、状态修订条件和唯一约束线性化；进程内任务锁只降低冲突频率，不作为正确性的唯一依据。

## 数据模型与持久化

1. CatalogRevision：revision_id、facade_zones、window_units、orientations、profile_batches、glass_batches、seal_batches、qualified_people、test_plans；各集合按业务键排序后生成摘要。
2. InspectionTask：task_id、generation、state、state_revision、locked_catalog_revision、locked_snapshot_hash、installation_confirmation、current_phase、terminal_kind、created_at、updated_at。状态限定为待锁定、试件安装中、气密加载中、水密喷淋中、抗风压复核中、待独立复核、可放行、已放行、返修隔离、已取消。
3. OccupancyToken：token_id、resource_kind、resource_id、task_id、generation、active、acquired_at、released_at；resource_kind 为窗单元、试验箱或位移测点，数据库对活动资源建立唯一约束。
4. PressureStep：phase、polarity、ordinal、target_pa、tolerance_pa、required_duration_seconds；StepRecord 保存 generation、实际压力、空气流量或位移、操作号、内容摘要和判定，唯一键约束同代次同阶段同序号。
5. SprayCheckpoint：checkpoint_id、pressure_ordinal、zone、start_offset_seconds、end_offset_seconds；SprayRecord 保存 generation、实测压力、传感器状态、观察结果和覆盖标记，覆盖集合只接受当前级次的完整成功回执。
6. InstrumentAttempt：attempt_id、operation_id、kind、request_hash、status、failure_code、response_payload、started_tick、completed_tick；失败尝试可审计且由显式命令重试，不自动伪造结果。
7. DefectEvidence：evidence_id、task_id、generation、defect_kind、window_unit_id、pressure_ordinal、measurement_point_id、version、immutable_payload_hash、supersedes_id、closure_id；同一版本写入后不可更新。
8. RepairGeneration：generation、parent_generation、affected_steps、affected_checkpoints、reason_evidence_ids；新代次复制未受影响的已完成义务，只重开确定的受影响范围。
9. Review：reviewer_id、qualification_revision、verdict_hash、decision、operation_id；同一自然人只能占一个复核席位。ReleaseCredential 保存 task_id、generation、verdict_hash、稳定序列号和签发时刻。
10. OperationResult 与 AuditEvent：分别保存操作号、规范化请求摘要、确定响应，以及事务内递增事件序号；服务重启后可恢复幂等结果、开放占用、阶段游标和终局。

## 公开接口

1. POST /api/v1/tasks：创建待锁定任务；POST /api/v1/tasks/{id}/lock：提交目录修订、窗单元、材料批次、方案、设备和测点并原子锁定。
2. POST /api/v1/tasks/{id}/installation：确认方向、安装、密封和测点归零；POST /api/v1/tasks/{id}/replace-chamber：仅在允许阶段原子更换试验箱。
3. POST /api/v1/tasks/{id}/pressure-steps：提交气密或抗风压级次及整数读数；POST /api/v1/tasks/{id}/spray-checkpoints：提交当前喷淋检查点。
4. POST /api/v1/tasks/{id}/instrument-attempts/{attemptID}/retry：显式重试失败调用；脚本化适配器通过进程内接口接收固定的成功、拒绝、断连、超时和畸形回执序列。
5. POST /api/v1/tasks/{id}/defects、/repairs、/defects/{evidenceID}/close：追加缺陷、开启返修代次及闭合证据；所有写请求包含 operation_id 和 expected_revision。
6. POST /api/v1/tasks/{id}/reviews：提交独立复核；POST /api/v1/tasks/{id}/release、/quarantine、/cancel：竞争唯一终局。GET /api/v1/tasks/{id} 返回锁定摘要、进度、覆盖、证据链、复核和凭据。
7. HTTP JSON 使用固定请求体上限、未知字段拒绝、字符串和数组数量限制；错误体包含 code、task_id、state_revision 和稳定排序的 reasons。
8. 真实质检操作页面仅提供任务锁定、占用查看、安装确认、逐级压力录入、喷淋检查、缺陷与返修、独立复核和终局操作；按当前状态禁用无效动作，并展示后端返回的修订冲突。
9. 仓库交付 go.mod、固定依赖版本和多阶段 Dockerfile；通过 docker buildx 构建 linux/amd64 与 linux/arm64，运行容器挂载 SQLite 数据目录并提供健康检查。

## 失败边界

1. 目录、任务、占用、采集、证据、复核、幂等结果和审计事件共享同一 SQLite 事务；任一校验或写入失败均整体回滚，响应使用已登记的稳定错误码。
2. 占用获取按资源种类和资源编号稳定排序后在一个事务内完成；唯一约束竞争转换为 OCCUPANCY_CONFLICT，并返回确定排序的冲突资源。
3. 外部试验箱、喷淋喷嘴和传感器位于事务之外。调用意图先持久化，回执再以独立事务核对任务代次、状态和 attempt 身份；迟到回执不能绕过代次及终态栅栏。
4. 同一 operation_id 的规范化请求摘要先于领域变更检查；摘要相同读取持久化结果，摘要不同返回 OPERATION_CONTENT_CONFLICT，重启后语义保持一致。
5. SQLite 忙、磁盘提交失败或注入式提交中断返回 STORE_UNAVAILABLE；内存只缓存只读目录，任务状态始终在提交后重新读取，避免发布未提交状态。
6. 启动恢复校验开放占用唯一性、每个阶段的已完成前缀、喷淋覆盖子集、证据链引用及唯一终局；发现破坏性不变量时拒绝启动并报告稳定恢复错误。

## 验收标准

1. 锁定操作在一个 SQLite 事务中冻结楼栋立面分区摘要、窗单元编号与朝向、型材/玻璃/密封材料批次及修订、试验箱、位移测点、气密与抗风压级次、喷淋检查点、持续秒数、判定阈值和不可复用任务代次；任一目录错配时整体拒绝。
2. 每个窗单元、试验箱和位移测点在开放任务中至多持有一个有效占用令牌；并发安装、换箱和启动由条件写入原子裁定，失败方不留下任何部分占用。
3. 安装与密封确认必须绑定锁定朝向、构造摘要和方案修订；压力加载严格形成不可跳跃前缀。相同操作号与相同内容返回原结果，不同内容返回稳定冲突码。
4. 气密与抗风压计算仅使用整数帕、立方厘米每秒和微米；采用明确的半向远离零舍入规则，并检查除零、加减乘溢出。算术失败不得写入读数结论或推进状态。
5. 喷淋提交必须匹配当前压力级次、检查点和锁定时间窗。仪器拒绝、断连、超时或格式错误只产生待重试调用记录，不能生成通过结果、推进覆盖集合或释放占用。
6. 渗水、残余变形或密封失效创建当前代次的不可覆盖证据版本；返修递增代次并重开受影响级次及检查点，旧代次迟到回执仅保留为拒绝事件，不参与当前结论。
7. 只有全部压力前缀与喷淋覆盖完成、整数指标满足锁定阈值、缺陷证据闭合，且两名不同合格人员分别绑定同一结论摘要复核后，才进入可放行并生成唯一放行凭据。
8. 放行、返修隔离和取消共享一个终局栅栏；并发竞争只有一个事务成功。终态后的加载、喷淋、仪器回执、返修及普通操作均返回确定错误且不改变持久状态。

## 确定性测试场景

1. catalog_lock_test.go：锁定虚构 A 座东立面 W-E-01 与正确材料批次成功，并精确断言规范化快照摘要及全部占用。
2. catalog_lock_test.go：楼栋分区、窗单元、朝向或任一材料批次错配时，断言稳定排序原因，且数据库无任务推进和部分占用。
3. occupancy_concurrency_test.go：两个任务通过同步屏障并发争抢同一试验箱和测点，精确断言仅一个成功，失败方占用数为零。
4. occupancy_concurrency_test.go：并发换箱与启动加载竞争，断言提交顺序只产生一个有效资源集合，旧箱不会被双重释放。
5. pressure_prefix_test.go：固定正负压级次中提交跳级、重复级和陈旧修订，断言游标、记录数及状态均不变。
6. pressure_prefix_test.go：相同操作号同内容重试返回字节等价结果；相同操作号改变整数读数返回 OPERATION_CONTENT_CONFLICT。
7. arithmetic_test.go：覆盖最大安全乘积、乘法溢出、除零和正负半值舍入，断言失败时没有派生结论。
8. spray_fault_test.go：使用假单调时钟验证检查点过早、超时和错误压力被拒绝，覆盖集合保持原值。
9. spray_fault_test.go：脚本化喷嘴依次返回拒绝、断连、超时、格式错误和成功，断言尝试次数、待重试状态及最终唯一覆盖。
10. repair_generation_test.go：渗水证据触发返修代次，只重开关联压力级次和喷淋检查点，未受影响完成项保持有效。
11. repair_generation_test.go：旧代次仪器成功回执晚到，断言证据链不可覆盖、当前覆盖不变并返回 STALE_GENERATION。
12. review_release_test.go：同一人员重复复核、资质过期和摘要不一致均被拒绝；两名合格人员对相同摘要复核后进入可放行。
13. terminal_race_test.go：用同步屏障并发执行放行、返修隔离和取消，多轮固定调度分别证明每种竞争胜者唯一且失败码确定。
14. terminal_race_test.go：终态后提交压力、喷淋、回执、返修和普通操作，逐项断言状态修订、记录数和占用均不改变。
15. recovery_test.go：在锁定、喷淋覆盖和放行提交点注入事务失败，重启后断言不存在部分令牌、检查点或凭据。
16. recovery_test.go：关闭并重开临时 SQLite 数据库，断言开放任务的压力前缀、喷淋覆盖、失败调用、证据代次、幂等结果及终局完整恢复。

## 组件追踪关系

1. 门窗构造与材料规则目录：负责立面分区、窗单元、朝向、材料批次、人员资质、试验方案和阈值的版本校验，支撑验收 1、3、7。
2. 联检任务聚合：实现十种状态、不可复用代次、锁定摘要、阶段守卫和终态封闭，支撑验收 1、3、6、8。
3. 试件/试验箱/测点占用账簿：保存一次性占用令牌及开放任务唯一约束，负责原子获取、换箱与终局释放，支撑验收 2、5、8。
4. 分级压力与喷淋采集账簿：维护有序加载前缀、喷淋覆盖集合、整数测量、外部调用尝试和待重试状态，支撑验收 3、4、5。
5. 缺陷证据及复核终局仲裁器：维护不可覆盖证据链、返修代次、复核身份分离、结论摘要和唯一终局凭据，支撑验收 6、7、8。
6. Go HTTP API 与真实质检操作页面：提供严格 JSON 命令、稳定错误响应及试件锁定、资源占用、分级录入、证据复核和放行界面，覆盖全部验收行为。

## 独特性

项目把已安装门窗的三类性能试验组合成同一冻结方案下的连续质量判定：压力级次是不可跳跃的有序前缀，定时喷淋是与当前压力绑定的覆盖集合，位移回弹和渗水观察进入跨返修代次的不可覆盖证据链。一次性试验箱与测点令牌、受影响义务的精确重开以及双人复核终局共同构成该领域特有的一致性模型。
