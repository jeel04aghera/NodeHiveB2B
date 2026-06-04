-- Built-in template registry (org_id NULL = available to every org). Idempotent.
-- These define the self-service environments employees can launch. Run in dev AND prod.

INSERT INTO templates (name, description, base_image, software, version, tags, default_expose_ssh, default_expose_jupyter) VALUES
 ('Ubuntu 22.04',
  'Clean Ubuntu 22.04 base. Bring your own toolchain over SSH.',
  'ubuntu:22.04',
  '[{"name":"Ubuntu","version":"22.04 LTS"},{"name":"OpenSSH","version":"8.9"}]'::jsonb,
  '22.04', '["base","ubuntu"]'::jsonb, true, false),

 ('Python 3.11',
  'Python 3.11 slim with pip. Good for general scripting and lightweight services.',
  'python:3.11-slim',
  '[{"name":"Python","version":"3.11"},{"name":"pip","version":"24.x"}]'::jsonb,
  '3.11', '["python","base"]'::jsonb, true, true),

 ('PyTorch 2.1 (CUDA 12.1)',
  'PyTorch 2.1 with CUDA 12.1 runtime and cuDNN 8. GPU training and inference.',
  'pytorch/pytorch:2.1.0-cuda12.1-cudnn8-runtime',
  '[{"name":"PyTorch","version":"2.1.0"},{"name":"CUDA","version":"12.1"},{"name":"cuDNN","version":"8"},{"name":"Python","version":"3.10"}]'::jsonb,
  '2.1.0', '["pytorch","cuda","gpu","ml"]'::jsonb, true, true),

 ('TensorFlow 2.15 (GPU)',
  'TensorFlow 2.15 GPU build with CUDA 12.2. Keras included.',
  'tensorflow/tensorflow:2.15.0-gpu',
  '[{"name":"TensorFlow","version":"2.15.0"},{"name":"CUDA","version":"12.2"},{"name":"Keras","version":"2.15"},{"name":"Python","version":"3.11"}]'::jsonb,
  '2.15.0', '["tensorflow","cuda","gpu","ml"]'::jsonb, true, true),

 ('CUDA 12.2 Devel',
  'NVIDIA CUDA 12.2 development image with nvcc + headers. Build CUDA from source.',
  'nvidia/cuda:12.2.0-devel-ubuntu22.04',
  '[{"name":"CUDA Toolkit","version":"12.2.0"},{"name":"nvcc","version":"12.2"},{"name":"Ubuntu","version":"22.04"}]'::jsonb,
  '12.2.0', '["cuda","devel","gpu"]'::jsonb, true, false),

 ('JupyterLab Data Science',
  'JupyterLab with pandas, numpy, scikit-learn, matplotlib. Notebook-first analysis.',
  'jupyter/datascience-notebook:latest',
  '[{"name":"JupyterLab","version":"4.x"},{"name":"pandas","version":"latest"},{"name":"numpy","version":"latest"},{"name":"scikit-learn","version":"latest"}]'::jsonb,
  'latest', '["jupyter","datascience","python"]'::jsonb, false, true)
ON CONFLICT (name) WHERE org_id IS NULL DO UPDATE SET
  description = EXCLUDED.description,
  base_image = EXCLUDED.base_image,
  software = EXCLUDED.software,
  version = EXCLUDED.version,
  tags = EXCLUDED.tags,
  default_expose_ssh = EXCLUDED.default_expose_ssh,
  default_expose_jupyter = EXCLUDED.default_expose_jupyter;
