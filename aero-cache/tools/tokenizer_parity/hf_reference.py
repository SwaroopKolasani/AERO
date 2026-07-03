import argparse
import json
from transformers import AutoTokenizer

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", required=True)
    parser.add_argument("--case", required=True)
    args = parser.parse_args()

    tok = AutoTokenizer.from_pretrained(args.model)

    with open(args.case, "r", encoding="utf-8") as f:
        case = json.load(f)

    ids = tok.apply_chat_template(
        case["messages"],
        tokenize=True,
        add_generation_prompt=True,
    )

    # Safe extraction: Handle BatchEncoding, dict payloads, or raw integer lists
    if hasattr(ids, "input_ids"):
        token_list = ids.input_ids
    elif isinstance(ids, dict) and "input_ids" in ids:
        token_list = ids["input_ids"]
    else:
        token_list = ids

    print(json.dumps({
        "model": args.model,
        "ids": token_list,
        "count": len(token_list)
    }))

if __name__ == "__main__":
    main()