#ifndef BACKEND_INFERENCE_BACKEND_H_
#define BACKEND_INFERENCE_BACKEND_H_

#include <functional>
#include <map>
#include <memory>
#include <string>
#include <vector>

enum class BackendKind {
  kSimulator,
  kTinyTextModel,
  kTinyLLM,
  kContinuousTinyLLM,
  kOnnxRuntime,
  kLlamaCpp,
  kLlamaServer,
};

enum class BackendStatusCode {
  kOk,
  kCancelled,
  kInvalidArgument,
  kUnavailable,
};

struct BackendRequest {
  std::string id;
  std::string prompt;
  std::string trace_id;
};

struct BackendResult {
  BackendStatusCode code = BackendStatusCode::kOk;
  std::string message;
  std::vector<std::string> outputs;
  std::vector<std::map<std::string, std::string>> output_metadata;
};

struct BackendStreamEvent {
  std::string event_type;
  std::string delta;
  std::string result;
  std::map<std::string, std::string> metadata;
};

using BackendStreamCallback =
    std::function<bool(const BackendStreamEvent& event)>;

struct BackendConfig {
  BackendKind kind = BackendKind::kSimulator;
  int simulator_base_latency_ms = 20;
  int simulator_per_request_latency_ms = 5;
  int tiny_model_iterations = 8;
  int tiny_llm_max_generated_tokens = 12;
  int tiny_llm_top_k = 5;
  float tiny_llm_temperature = 0.8f;
  int continuous_max_prefill_tokens_per_step = 32;
  int continuous_max_decode_sequences_per_step = 8;
  int continuous_kv_cache_blocks = 512;
  int continuous_kv_block_tokens = 16;
  int continuous_prefix_cache_tokens = 2;
  std::string onnx_model_path = "models/logreg_iris.onnx";
  std::string onnx_runtime_library_path;
  std::string llama_cpp_binary_path = "llama-cli";
  std::string llama_cpp_model_path;
  int llama_cpp_max_tokens = 64;
  int llama_cpp_context_size = 2048;
  int llama_cpp_threads = 0;
  float llama_cpp_temperature = 0.7f;
  int llama_cpp_timeout_ms = 120000;
  std::string llama_server_url = "http://127.0.0.1:8080/completion";
  int llama_server_max_tokens = 64;
  float llama_server_temperature = 0.7f;
  int llama_server_timeout_ms = 120000;
  bool llama_server_cache_prompt = true;
  bool llama_server_stream = true;
  bool llama_server_timings_per_token = true;
};

class InferenceBackend {
 public:
  virtual ~InferenceBackend() = default;

  virtual std::string Name() const = 0;
  virtual BackendResult ProcessBatch(
      const std::vector<BackendRequest>& requests,
      const std::function<bool()>& is_cancelled) = 0;
  virtual BackendResult ProcessStream(
      const BackendRequest& request, const std::function<bool()>& is_cancelled,
      const BackendStreamCallback& emit);
};

class SimulatorBackend final : public InferenceBackend {
 public:
  SimulatorBackend(int base_latency_ms, int per_request_latency_ms);

  std::string Name() const override;
  BackendResult ProcessBatch(
      const std::vector<BackendRequest>& requests,
      const std::function<bool()>& is_cancelled) override;

 private:
  int base_latency_ms_;
  int per_request_latency_ms_;
};

class TinyTextModelBackend final : public InferenceBackend {
 public:
  explicit TinyTextModelBackend(int iterations);

  std::string Name() const override;
  BackendResult ProcessBatch(
      const std::vector<BackendRequest>& requests,
      const std::function<bool()>& is_cancelled) override;

 private:
  int iterations_;
};

class TinyLLMBackend final : public InferenceBackend {
 public:
  TinyLLMBackend(int max_generated_tokens, float temperature, int top_k);

  std::string Name() const override;
  BackendResult ProcessBatch(
      const std::vector<BackendRequest>& requests,
      const std::function<bool()>& is_cancelled) override;

 private:
  int max_generated_tokens_;
  float temperature_;
  int top_k_;
};

class ContinuousTinyLLMBackend final : public InferenceBackend {
 public:
  ContinuousTinyLLMBackend(int max_generated_tokens, float temperature,
                           int top_k, int max_prefill_tokens_per_step,
                           int max_decode_sequences_per_step,
                           int kv_cache_blocks, int kv_block_tokens,
                           int prefix_cache_tokens);

  std::string Name() const override;
  BackendResult ProcessBatch(
      const std::vector<BackendRequest>& requests,
      const std::function<bool()>& is_cancelled) override;

 private:
  int max_generated_tokens_;
  float temperature_;
  int top_k_;
  int max_prefill_tokens_per_step_;
  int max_decode_sequences_per_step_;
  int kv_cache_blocks_;
  int kv_block_tokens_;
  int prefix_cache_tokens_;
};

class OnnxRuntimeBackend final : public InferenceBackend {
 public:
  OnnxRuntimeBackend(std::string model_path, std::string runtime_library_path);
  ~OnnxRuntimeBackend() override;

  OnnxRuntimeBackend(const OnnxRuntimeBackend&) = delete;
  OnnxRuntimeBackend& operator=(const OnnxRuntimeBackend&) = delete;

  std::string Name() const override;
  BackendResult ProcessBatch(
      const std::vector<BackendRequest>& requests,
      const std::function<bool()>& is_cancelled) override;

 private:
  struct Impl;
  std::unique_ptr<Impl> impl_;
};

class LlamaCppBackend final : public InferenceBackend {
 public:
  LlamaCppBackend(std::string binary_path, std::string model_path,
                  int max_tokens, int context_size, int threads,
                  float temperature, int timeout_ms);

  std::string Name() const override;
  BackendResult ProcessBatch(
      const std::vector<BackendRequest>& requests,
      const std::function<bool()>& is_cancelled) override;

 private:
  std::string binary_path_;
  std::string model_path_;
  int max_tokens_;
  int context_size_;
  int threads_;
  float temperature_;
  int timeout_ms_;
};

class LlamaServerBackend final : public InferenceBackend {
 public:
  LlamaServerBackend(std::string server_url, int max_tokens,
                     float temperature, int timeout_ms, bool cache_prompt,
                     bool stream, bool timings_per_token);

  std::string Name() const override;
  BackendResult ProcessBatch(
      const std::vector<BackendRequest>& requests,
      const std::function<bool()>& is_cancelled) override;
  BackendResult ProcessStream(
      const BackendRequest& request, const std::function<bool()>& is_cancelled,
      const BackendStreamCallback& emit) override;

 private:
  std::string server_url_;
  int max_tokens_;
  float temperature_;
  int timeout_ms_;
  bool cache_prompt_;
  bool stream_;
  bool timings_per_token_;
};

BackendKind ParseBackendKind(const std::string& raw);
std::string BackendKindName(BackendKind kind);
BackendConfig BackendConfigFromEnv();
std::unique_ptr<InferenceBackend> CreateBackend(const BackendConfig& config);

#endif  // BACKEND_INFERENCE_BACKEND_H_
