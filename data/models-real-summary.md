# 真实模型数据摘要

来源：`https://openrouter.ai/api/v1/models`（HTTP 200，公开端点，无认证），原始数据 `data/models-real.json`。

- 模型总数：**460**
- 供应商数：**69**

## 真实样例（前 30 条，字段逐字取自上游 JSON）

| id | name | context_length | prompt/token | completion/token | modality |
|---|---|---|---|---|---|
| `liquid/lfm-3-1.7b` | LiquidAI: LFM3-1.7B | 262144 | 0.00000008 | 0.0000002 | text->text |
| `liquid/lfm-3-1.7b:free` | LiquidAI: LFM3-1.7B (free) | 262144 | 0 | 0 | text->text |
| `google/gemini-omni-1.2-flash` | Google: Gemini Omni 1.2 Flash | 131072 | 0.0000015 | 0.000009 | text+image+video->text+video |
| `google/gemini-omni-1.1-flash` | Google: Gemini Omni 1.1 Flash | 131072 | 0.0000015 | 0.000009 | text+image+video->text+video |
| `ricursiveai/ripple-65b-preview` | Ricursive: Ripple 65B Preview | 1048576 | 0.0000002 | 0.0000006 | text->text |
| `tencent/hy4` | Tencent: Hy4 | 1048576 | 0.000000417 | 0.0000012505 | text+image->text |
| `stealth/crouton-alpha` | Crouton Alpha | 1048576 | 0 | 0 | text+image+file+audio+video->text |
| `z-ai/glm-5.3-flashx-legal` | Z.ai: GLM 5.3 FlashX Legal | 1048576 | 0.00000037 | 0.00000125 | text+image+video->text |
| `particle-ai/particle-1` | Particle AI: Particle-1 | 1048576 | 0.00000005 | 0.0000005 | text+image->text |
| `openai/gpt-6.1-terra-pro` | OpenAI: GPT-6.1 Terra Pro | 1050000 | 0.000001 | 0.000005 | text+image+file->text |
| `openai/gpt-6.1-terra-pro:batch` | OpenAI: GPT-6.1 Terra Pro (batch) | 1050000 | 0.0000005 | 0.0000025 | text+image+file->text |
| `openai/gpt-6.1-terra` | OpenAI: GPT-6.1 Terra | 1050000 | 0.000001 | 0.000005 | text+image+file->text |
| `openai/gpt-6.1-terra:batch` | OpenAI: GPT-6.1 Terra (batch) | 1050000 | 0.0000005 | 0.0000025 | text+image+file->text |
| `moonshotai/kimi-k3.1-pro` | MoonshotAI: Kimi K3.1 Pro | 1048576 | 0.000002 | 0.00001 | text+image+video->text |
| `recursive/flint-v1.0:free` | Recursive: Flint (free) | 262144 | 0 | 0 | text->text |
| `minimax/minimax-m3.1-flash` | MiniMax: MiniMax M3.1 Flash | 1048576 | 0.00000015 | 0.0000006 | text+image+video->text |
| `deepseek/deepseek-v4.1-flash-1017` | DeepSeek: DeepSeek V4.1 Flash 1017 | 1048576 | 0.00000015 | 0.0000006 | text+image->text |
| `elorian/glimpse` | Elorian: Glimpse | 262144 | 0.0000006 | 0.000003 | text+image+video->text |
| `xingchen-agi/xing4.0-29b-a4b:free` | XingChen: Xing4.0 29B A4B (free) | 262144 | 0 | 0 | text->text |
| `reflection/beam-1.0-501b-a23b` | Reflection: Beam 1.0 501B A23B | 1048576 | 0.0000003 | 0.000001 | text->text |
| `inception/mercury-3` | Inception: Mercury 3 | 1000000 | 0.0000001 | 0.00000035 | text->text |
| `prism-ml/ternary-bonsai-2-9b` | PrismML: Ternary Bonsai 2 9B | 262144 | 0.00000008 | 0.00000013 | text+image->text |
| `moondream/moondream3-preview` | Moondream: Moondream 3 Preview | 32000 | 0.0000003 | 0.0000025 | text+image->text |
| `moondream/moondream3.1-9b-a2b` | Moondream: Moondream 3.1 | 32000 | 0.0000003 | 0.0000025 | text+image->text |
| `meta/muse-spark-open` | Meta: Muse Spark Open | 1048576 | 0.00000125 | 0.00000425 | text+image+file+audio+video->text |
| `nvidia/nemotron-3.5-super-vl` | NVIDIA: Nemotron 3.5 Super VL | 1048576 | 0.00000015 | 0.0000005 | text+image+video->text |
| `nvidia/nemotron-3.5-super-vl:free` | NVIDIA: Nemotron 3.5 Super VL (free) | 1000000 | 0 | 0 | text+image+video->text |
| `unbiased/pareto-26.10` | Pareto 26.10 | 1048576 | 0.0000008 | 0.0000032 | text+image->text |
| `~unbiased/pareto-latest` | Pareto Latest | 1048576 | 0.0000008 | 0.0000032 | text+image->text |
| `unbiased/pareto-26.9` | Pareto 26.9 | 262144 | 0.0000025 | 0.0000075 | text+image->text |

## 供应商分布（真实统计）

- `openai`: 98
- `google`: 43
- `qwen`: 43
- `anthropic`: 30
- `mistralai`: 21
- `z-ai`: 19
- `deepseek`: 16
- `nvidia`: 11
- `x-ai`: 10
- `moonshotai`: 9
- `meta`: 9
- `tencent`: 8
- `meta-llama`: 8
- `openrouter`: 7
- `minimax`: 6
- `aion-labs`: 6
- `cohere`: 6
- `bytedance-seed`: 6
- `liquid`: 5
- `xiaomi`: 5
