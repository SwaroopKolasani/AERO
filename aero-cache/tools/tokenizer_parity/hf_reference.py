import argparse
import json
from pathlib import Path

from transformers import AutoTokenizer


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", required=True)
    parser.add_argument("--case", required=True)
    parser.add_argument("--date-string", default="04 Jul 2026")
    parser.add_argument("--write", action="store_true")
    args = parser.parse_args()

    case_path = Path(args.case)

    tok = AutoTokenizer.from_pretrained(args.model)

    with case_path.open("r", encoding="utf-8") as f:
        case = json.load(f)

    ids = tok.apply_chat_template(
        case["messages"],
        tokenize=True,
        add_generation_prompt=True,
        date_string=args.date_string,
    )

    if hasattr(ids, "input_ids"):
        token_list = ids.input_ids
    elif isinstance(ids, dict) and "input_ids" in ids:
        token_list = ids["input_ids"]
    else:
        token_list = ids

    out = {
        "model": args.model,
        "date_string": args.date_string,
        "ids": token_list,
        "count": len(token_list),
    }

    if args.write:
        case["model"] = args.model
        case["date_string"] = args.date_string
        case["expected_tokens"] = token_list
        case["expected_count"] = len(token_list)

        with case_path.open("w", encoding="utf-8") as f:
            json.dump(case, f, indent=2)
            f.write("\n")

    print(json.dumps(out))


if __name__ == "__main__":
    main()