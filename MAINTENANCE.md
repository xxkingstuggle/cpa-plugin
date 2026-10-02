# 自维护约定

## 源码和历史

- 维护仓库：https://github.com/xxkingstuggle/cpa-plugin
- 长期维护分支：`personal/custom`。
- 原始上游：https://github.com/Sliverkiss/cpa-plugin 。2026-10-02 实时访问仓库、版本文件和 registry 均返回 404；历史索引显示曾归档，原因未知。
- 本地仓库已从自己的 origin 补齐 Git 历史，不再是 shallow clone。上游不可访问期间，以已保存的源码和历史为维护基础。
- 保留 WorkBuddy 和 QoderWork 原源码、原作者信息及各自 LICENSE。WorkBuddy 使用 MIT License。

## 维护基线

`personal/custom` 起点为 `5b710b1`，包含原 WorkBuddy 0.9.3、Hermes 分支的配置重载修复和 CPA v8 流式协议回归测试。

当前部署的插件元数据版本为 0.9.3；源码 VERSION 为 0.9.3-hermes.1，两者存在已有差异。建立维护分支本身不会发布新版本或替换部署文件。

2026-10-02 的原生测试基线：WorkBuddy Go 测试 562 通过、1 失败、1 跳过，Node 测试 18 通过。已有失败是 `TestDefaultVersionMatchesVersionFile`（上述版本差异），跳过是 macOS 不适用的 Windows 路由测试；此次新增协议测试无失败、无跳过。不得将其描述为全套测试通过。

## 开发和发布

1. 后续修改从 `personal/custom` 建立任务分支，提交审核后的修复再合并回维护分支。
2. WorkBuddy 验证使用模块内 `go test ./...` 以及项目已有面板测试。修复已有版本差异后，发布前必须通过全部适用测试。
3. 版本文件、注册元数据和发布 tag 必须一致；自维护版使用独立后缀，避免冒充原作者发行版。
4. 保留 `.github/workflows/build.yml` 的多架构构建及 release 流程；tag 形如 `workbuddy-v<version>`。在自己的仓库发布并验证产物前，不启用自动更新源。
5. 部署先保存旧插件和配置，在隔离 CPA 实例验证注册、模型、流式文本、工具调用、取消和错误路径，再替换现网并做真实客户端验收。
6. 凭据、OAuth 文件、真实配置、编译产物不得提交 Git。原作者的 registry 下载地址不属于本仓库发布渠道。
