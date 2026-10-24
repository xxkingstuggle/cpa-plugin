# 项目指引

## 项目定位
为 CLIProxyAPI 提供 WorkBuddy/CodeBuddy 与 QoderWork CN OAuth Provider 插件。

## 如何运行
先看 README.md 的构建与 Release 约定；每个插件分别位于 workbuddy/ 和 qoderwork/，各自的 Makefile 定义构建入口。

## 技术栈
Go 插件、少量 Python 校验脚本与 GitHub Actions 多平台构建。

## 目录与约定
插件源码与测试位于对应插件目录；registry.json 是插件商店索引，发布版本遵循 README.md 的命名。

## 当前状态与下一步
发布前核对 registry、插件版本和各平台产物命名；不要把 OAuth 状态、个人日志或本机运行数据加入仓库。
