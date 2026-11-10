# 流程画布与接口管理实施计划

**Goal:** 把 LogicFlow 编辑和 Scalar 接口管理接入当前 Go/Vue/MySQL master。

**Spec:** ../specs/2026-11-05-jmeter-workbench-design.md

**Ownership:** workflow/** 前端画布；api-management/** Scalar 前端；manager-go/** Go域/表/API/编译与代理；root处理共享导航/依赖/集成/部署/浏览器。不得互相覆盖其他工作包。

- [x] 核对官方依赖和实际导出，固定 LogicFlow2.2.5、Scalar3.23.6、yaml2.9.1；冻结前后端DTO。
- [x] LogicFlow画布、参数表单、树约束、保存/OCC、JMX导入导出发布和测试。
- [x] Scalar真实组件、中文、集合/认证保存、双请求模式、导入导出/版本与测试。
- [x] Go工作流/集合/版本表、加密、迁移备份兼容、JMX编译和受限代理，测试。
- [x] root共享导航和懒加载、整体测试构建、独立代码审查。
- [x] 备份/部署新版本；真实MySQL、画布JMX六节点内部执行、接口双模式与浏览器验收。
- [x] 完成文档/资产清单/源码校验/最终运行状态记录。

后续用户要求已落实为融合脚本工作区、四视图与可选主题，最终交付以[统一工作区任务清单](2026-11-05-jmeter-unified-workspace.md)为准。
