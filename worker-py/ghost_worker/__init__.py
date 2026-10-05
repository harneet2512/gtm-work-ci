"""Ghost model-facing worker (HAR-96 WP3/WP7)."""
import os

# litellm fetches a remote model-cost map at import time unless told to use its bundled copy.
# Set before anything can import litellm so replay/test runs never touch the network.
os.environ.setdefault("LITELLM_LOCAL_MODEL_COST_MAP", "True")
