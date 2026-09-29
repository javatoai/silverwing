# Git 发布与构建规则

- 远程仓库：`origin` 为 GitLab，`github` 为 GitHub。
- 用户明确授权发布提交后，必须将同一分支推送到 GitLab 和 GitHub；任一远程推送失败时，不能将发布报告为完成。
- 用户明确授权创建 Tag 后，必须将同一个 Tag 推送到 GitLab 和 GitHub，并核对两个远程都指向预期提交。
- GitLab 是代码镜像；仅 GitHub 会触发 GitHub Actions 构建、打包或发布。不要将 GitLab 推送视为构建触发条件。
- 最终报告应分别说明 GitLab、GitHub 的分支或 Tag 推送结果，以及 GitHub Actions 的构建状态（如本次涉及构建）。
