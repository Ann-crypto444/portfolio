# Fine-tuning BERT (Transformers + Trainer) — IMDB / SST-2

This is a minimal starter to fine-tune a BERT-like model for text classification using Hugging Face **Transformers** + **Trainer**.

## 1) Setup

```bash
# (Recommended) Create a virtual env
python -m venv .venv && source .venv/bin/activate  # Windows: .venv\Scripts\activate

# Install PyTorch first (choose CUDA/CPU build): https://pytorch.org/get-started/locally/
pip install torch --index-url https://download.pytorch.org/whl/cpu  # or your CUDA index

# Install the rest
pip install -r requirements.txt
```

## 2) Train

IMDB (binary classification):
```bash
python train_text_classification.py --model bert-base-uncased --dataset imdb --epochs 2
```

SST-2 (GLUE):
```bash
python train_text_classification.py --model bert-base-uncased --dataset glue:sst2 --epochs 3
```

Tip: try a smaller model for faster training:
```bash
python train_text_classification.py --model distilbert-base-uncased --dataset imdb --epochs 2 --batch 16
```

Outputs (best model) will be saved to `./model_out/` in HF format via `.save_pretrained`.

## 3) Quick inference test

```python
from transformers import AutoTokenizer, AutoModelForSequenceClassification
import torch

model_dir = "model_out"
tok = AutoTokenizer.from_pretrained(model_dir)
mdl = AutoModelForSequenceClassification.from_pretrained(model_dir)
inp = tok("I really enjoyed this movie!", return_tensors="pt")
with torch.no_grad():
    logits = mdl(**inp).logits
print(logits.softmax(-1))
```

## 4) Notes
- The script uses `evaluate` for accuracy and `sklearn` for F1.
- Uses `DataCollatorWithPadding` for dynamic padding.
- `load_best_model_at_end=True` selects the best checkpoint on validation accuracy.
- Reproducibility: `seed=42`.
- You can swap datasets or bring your own CSV; map your text column to `text` and labels to `label`.

## 5) Next steps (optional)
- Wrap inference in **FastAPI** and then **Dockerize**.
- Push the model to your private/public Hugging Face Hub.
