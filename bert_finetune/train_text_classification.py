#!/usr/bin/env python
"""
Fine-tune a BERT-like model on a text classification dataset (IMDB by default) using Hugging Face Trainer.
Usage (IMDB example):
  python train_text_classification.py --model bert-base-uncased --dataset imdb --epochs 2
  python train_text_classification.py --model distilbert-base-uncased --dataset imdb --epochs 2

SST-2 (GLUE) example:
  python train_text_classification.py --model bert-base-uncased --dataset glue:sst2 --epochs 3
"""
import argparse
from dataclasses import dataclass
from typing import Dict, List, Optional

import numpy as np
from datasets import load_dataset, DatasetDict
from transformers import (
    AutoTokenizer,
    AutoModelForSequenceClassification,
    DataCollatorWithPadding,
    TrainingArguments,
    Trainer,
)
import evaluate
from sklearn.metrics import f1_score

TEXT_COL = "text"
LABEL_COL = "label"

def get_dataset(name: str):
    """Load dataset by a short name.
    Supports 'imdb' or 'glue:sst2' (SST-2). Returns DatasetDict with train/validation/test.
    """
    if name.lower() == "imdb":
        ds_full = load_dataset("imdb")
        ds_full = ds_full.rename_column("text", TEXT_COL).rename_column("label", LABEL_COL)
        # Split original train into train/validation
        split = ds_full["train"].train_test_split(test_size=0.1, seed=42, stratify_by_column=LABEL_COL)
        train = split["train"]
        validation = split["test"]
        test = ds_full["test"]
        return {"train": train, "validation": validation, "test": test}
    elif name.lower().startswith("glue:") and name.lower().split(":")[1] == "sst2":
        glue = load_dataset("glue", "sst2")
        glue = glue.rename_column("sentence", TEXT_COL).rename_column("label", LABEL_COL)
        return {"train": glue["train"], "validation": glue["validation"], "test": glue["test"]}
    else:
        raise ValueError("Unsupported dataset. Use 'imdb' or 'glue:sst2'.")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", type=str, default="bert-base-uncased", help="HF model id (e.g. bert-base-uncased)")
    parser.add_argument("--dataset", type=str, default="imdb", help="'imdb' or 'glue:sst2'")
    parser.add_argument("--epochs", type=int, default=2)
    parser.add_argument("--batch", type=int, default=8, help="per-device batch size")
    parser.add_argument("--lr", type=float, default=2e-5)
    parser.add_argument("--wd", type=float, default=0.01, help="weight decay")
    parser.add_argument("--out_dir", type=str, default="./model_out")
    args = parser.parse_args()

    print(f"Loading dataset: {args.dataset}")
    ds = get_dataset(args.dataset)

    print(f"Loading tokenizer: {args.model}")
    tokenizer = AutoTokenizer.from_pretrained(args.model, use_fast=True)

    def tokenize(batch):
        return tokenizer(batch[TEXT_COL], truncation=True)

    train_tok = ds["train"].map(tokenize, batched=True, remove_columns=[TEXT_COL])
    val_tok   = ds["validation"].map(tokenize, batched=True, remove_columns=[TEXT_COL])
    test_tok  = ds["test"].map(tokenize, batched=True, remove_columns=[TEXT_COL])
    data_collator = DataCollatorWithPadding(tokenizer=tokenizer)

    # infer num_labels
    num_labels = len(set(train_tok[LABEL_COL]))
    print(f"Detected num_labels={num_labels}")

    print(f"Loading model: {args.model}")
    model = AutoModelForSequenceClassification.from_pretrained(args.model, num_labels=num_labels)

    accuracy = evaluate.load("accuracy")

    def compute_metrics(eval_pred):
        logits, labels = eval_pred
        preds = np.argmax(logits, axis=-1)
        acc = accuracy.compute(predictions=preds, references=labels)["accuracy"]
        try:
            from sklearn.metrics import f1_score
            f1 = f1_score(labels, preds, average="weighted")
        except Exception:
            f1 = 0.0
        return {"accuracy": acc, "f1": f1}

    training_args = TrainingArguments(
        output_dir=args.out_dir,
        evaluation_strategy="epoch",
        save_strategy="epoch",
        learning_rate=args.lr,
        per_device_train_batch_size=args.batch,
        per_device_eval_batch_size=args.batch,
        num_train_epochs=args.epochs,
        weight_decay=args.wd,
        logging_steps=50,
        load_best_model_at_end=True,
        metric_for_best_model="accuracy",
        report_to="none",
        seed=42,
    )

    trainer = Trainer(
        model=model,
        args=training_args,
        train_dataset=train_tok,
        eval_dataset=val_tok,
        tokenizer=tokenizer,
        data_collator=data_collator,
        compute_metrics=compute_metrics,
    )

    print("\n*** TRAINING ***\n")
    trainer.train()

    print("\n*** EVALUATION (validation) ***\n")
    metrics_val = trainer.evaluate()
    print(metrics_val)

    print("\n*** EVALUATION (test) ***\n")
    test_metrics = trainer.evaluate(test_tok)
    print(test_metrics)

    # Save best model & tokenizer (Hugging Face format)
    print(f"\n*** SAVING to {args.out_dir} ***\n")
    trainer.save_model(args.out_dir)            # saves model + tokenizer via trainer
    tokenizer.save_pretrained(args.out_dir)

    print("Done. You can load with AutoModelForSequenceClassification.from_pretrained(out_dir)")


if __name__ == "__main__":
    main()
