# UnifiedInfraDiagnosis

诊断编排层：LangGraph 状态机执行确定性判定序，消费 OKB REST 四原语完成跨域故障诊断。

- 记忆底座：[UnifiedInfraOKB](../UnifiedInfraOKB)（lookup/trace/evidence/report）
- 判定序依据：[跨域关联全景](../UnifiedInfrastructureModel/跨域关联全景.md)
- 协作约束：[AGENTS.md](AGENTS.md)

## 快速开始

```bash
uv sync                                  # 安装依赖
uv run pytest                            # 回归测试（需 OKB server 在 18090，或用 mock）
uv run python -m diagnosis.cli "T30alertPsuFault"   # 跑一次掉电诊断
```

## 结构

```
diagnosis/
├── tools/okb.py          # OKB REST 客户端（四原语封装）
├── graphs/rack_power.py  # 机柜掉电判定序（金路径场景）
└── cli.py                # 命令行入口
tests/                    # 判定序回归测试（金路径断言）
```
