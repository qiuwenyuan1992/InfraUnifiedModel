"""命令行入口：跑一次掉电诊断并打印结论。"""

from __future__ import annotations

import argparse
import json

from diagnosis.graphs.rack_power import diagnose


def main() -> None:
    parser = argparse.ArgumentParser(description="机柜掉电判定序诊断")
    parser.add_argument("alert", help="告警 signature，如 T30alertPsuFault")
    args = parser.parse_args()
    state = diagnose(args.alert)
    print(json.dumps(state.get("conclusion", state), ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
