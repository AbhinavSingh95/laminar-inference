#include "backend/inference_backend.h"

#include <atomic>
#include <chrono>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <mutex>
#include <netinet/in.h>
#include <optional>
#include <sstream>
#include <string>
#include <sys/socket.h>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>
#include <vector>

namespace {

int failures = 0;

void Expect(bool condition, const std::string& message) {
  if (!condition) {
    std::cerr << "FAIL: " << message << std::endl;
    ++failures;
  }
}

std::vector<BackendRequest> Requests(std::initializer_list<std::string> prompts) {
  std::vector<BackendRequest> requests;
  int id = 1;
  for (const auto& prompt : prompts) {
    BackendRequest request;
    request.id = "req-" + std::to_string(id);
    request.prompt = prompt;
    request.trace_id = "trace-" + std::to_string(id);
    requests.push_back(request);
    ++id;
  }
  return requests;
}

std::optional<std::string> ExtractField(const std::string& text,
                                        const std::string& prefix,
                                        const std::string& suffix) {
  const size_t start = text.find(prefix);
  if (start == std::string::npos) {
    return std::nullopt;
  }
  const size_t value_start = start + prefix.size();
  const size_t value_end = text.find(suffix, value_start);
  if (value_end == std::string::npos) {
    return std::nullopt;
  }
  return text.substr(value_start, value_end - value_start);
}

std::filesystem::path TestTempDir(const std::string& name) {
  std::filesystem::path path =
      std::filesystem::temp_directory_path() /
      ("laminar-inference-" + name + "-" + std::to_string(getpid()));
  std::filesystem::remove_all(path);
  std::filesystem::create_directories(path);
  return path;
}

std::filesystem::path WriteFakeLlamaCli(const std::filesystem::path& dir) {
  const std::filesystem::path script = dir / "fake-llama-cli";
  std::ofstream out(script);
  out << "#!/usr/bin/env bash\n"
      << "set -euo pipefail\n"
      << "prompt=''\n"
      << "model=''\n"
      << "tokens=''\n"
      << "while [[ $# -gt 0 ]]; do\n"
      << "  case \"$1\" in\n"
      << "    -p|--prompt) prompt=\"$2\"; shift 2 ;;\n"
      << "    -m|--model) model=\"$2\"; shift 2 ;;\n"
      << "    -n|--predict) tokens=\"$2\"; shift 2 ;;\n"
      << "    *) shift ;;\n"
      << "  esac\n"
      << "done\n"
      << "printf 'fake llama completion for %s using %s max=%s\\n' "
         "\"$prompt\" \"${model##*/}\" \"$tokens\"\n";
  out.close();
  chmod(script.c_str(), 0755);
  return script;
}

class FakeLlamaServer {
 public:
  explicit FakeLlamaServer(std::string response_body)
      : response_body_(std::move(response_body)) {
    listen_fd_ = socket(AF_INET, SOCK_STREAM, 0);
    Expect(listen_fd_ >= 0, "fake llama server socket should open");

    int reuse = 1;
    setsockopt(listen_fd_, SOL_SOCKET, SO_REUSEADDR, &reuse, sizeof(reuse));

    sockaddr_in addr{};
    addr.sin_family = AF_INET;
    addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    addr.sin_port = 0;
    Expect(bind(listen_fd_, reinterpret_cast<sockaddr*>(&addr), sizeof(addr)) == 0,
           "fake llama server should bind");
    Expect(listen(listen_fd_, 8) == 0, "fake llama server should listen");

    socklen_t len = sizeof(addr);
    Expect(getsockname(listen_fd_, reinterpret_cast<sockaddr*>(&addr), &len) == 0,
           "fake llama server should expose port");
    port_ = ntohs(addr.sin_port);
    thread_ = std::thread([this] { Serve(); });
  }

  ~FakeLlamaServer() {
    stop_.store(true);
    const int fd = socket(AF_INET, SOCK_STREAM, 0);
    if (fd >= 0) {
      sockaddr_in addr{};
      addr.sin_family = AF_INET;
      addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
      addr.sin_port = htons(port_);
      connect(fd, reinterpret_cast<sockaddr*>(&addr), sizeof(addr));
      close(fd);
    }
    if (thread_.joinable()) {
      thread_.join();
    }
    if (listen_fd_ >= 0) {
      close(listen_fd_);
    }
  }

  std::string Url() const {
    return "http://127.0.0.1:" + std::to_string(port_) + "/completion";
  }

  std::string LastRequest() const {
    std::lock_guard<std::mutex> lock(mu_);
    return last_request_;
  }

 private:
  void Serve() {
    while (!stop_.load()) {
      const int client = accept(listen_fd_, nullptr, nullptr);
      if (client < 0) {
        continue;
      }
      std::string request;
      char buffer[4096];
      while (true) {
        const ssize_t bytes = recv(client, buffer, sizeof(buffer), 0);
        if (bytes <= 0) {
          break;
        }
        request.append(buffer, static_cast<size_t>(bytes));
        if (request.find("\r\n\r\n") != std::string::npos) {
          break;
        }
      }
      {
        std::lock_guard<std::mutex> lock(mu_);
        last_request_ = request;
      }
      const std::string response =
          "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " +
          std::to_string(response_body_.size()) +
          "\r\nConnection: close\r\n\r\n" + response_body_;
      send(client, response.data(), response.size(), 0);
      close(client);
    }
  }

  std::string response_body_;
  int listen_fd_ = -1;
  uint16_t port_ = 0;
  std::atomic<bool> stop_{false};
  mutable std::mutex mu_;
  std::string last_request_;
  std::thread thread_;
};

void TestBackendModeParsing() {
  Expect(ParseBackendKind("simulator") == BackendKind::kSimulator,
         "simulator mode should parse");
  Expect(ParseBackendKind("tiny_model") == BackendKind::kTinyTextModel,
         "tiny_model mode should parse");
  Expect(ParseBackendKind("tiny-text") == BackendKind::kTinyTextModel,
         "tiny-text alias should parse");
  Expect(ParseBackendKind("onnx") == BackendKind::kOnnxRuntime,
         "onnx mode should parse");
  Expect(ParseBackendKind("onnxruntime") == BackendKind::kOnnxRuntime,
         "onnxruntime alias should parse");
  Expect(ParseBackendKind("tiny_llm") == BackendKind::kTinyLLM,
         "tiny_llm mode should parse");
  Expect(ParseBackendKind("tiny-llm") == BackendKind::kTinyLLM,
         "tiny-llm alias should parse");
  Expect(ParseBackendKind("continuous_tiny_llm") ==
             BackendKind::kContinuousTinyLLM,
         "continuous_tiny_llm mode should parse");
  Expect(ParseBackendKind("continuous-llm") == BackendKind::kContinuousTinyLLM,
         "continuous-llm alias should parse");
  Expect(ParseBackendKind("llama_cpp") == BackendKind::kLlamaCpp,
         "llama_cpp mode should parse");
  Expect(ParseBackendKind("llama-cpp") == BackendKind::kLlamaCpp,
         "llama-cpp alias should parse");
  Expect(ParseBackendKind("llama_server") == BackendKind::kLlamaServer,
         "llama_server mode should parse");
  Expect(ParseBackendKind("llama-server") == BackendKind::kLlamaServer,
         "llama-server alias should parse");
  Expect(ParseBackendKind("unknown") == BackendKind::kSimulator,
         "unknown mode should fall back to simulator");
}

void TestOnnxRuntimeBackendIsExplicitlyConfigured() {
  BackendConfig config;
  config.kind = BackendKind::kOnnxRuntime;
  config.onnx_model_path = "models/logreg_iris.onnx";
  config.onnx_runtime_library_path = "/missing/libonnxruntime.dylib";

  std::unique_ptr<InferenceBackend> backend = CreateBackend(config);
  BackendResult result = backend->ProcessBatch(
      Requests({"setosa petal benchmark"}), [] { return false; });

  Expect(result.code == BackendStatusCode::kUnavailable,
         "onnx backend should report unavailable when runtime library is missing");
  Expect(result.message.find("libonnxruntime") != std::string::npos,
         "onnx unavailable message should mention runtime library");
}

void TestSimulatorBackendIsDeterministic() {
  SimulatorBackend backend(/*base_latency_ms=*/0, /*per_request_latency_ms=*/0);
  BackendResult result = backend.ProcessBatch(
      Requests({"hello", "world"}), [] { return false; });

  Expect(result.code == BackendStatusCode::kOk, "simulator should succeed");
  Expect(result.outputs.size() == 2, "simulator should return one output per input");
  Expect(result.outputs[0].find("backend=simulator") != std::string::npos,
         "simulator output should identify backend");
  Expect(result.outputs[0].find("batch_size=2") != std::string::npos,
         "simulator output should include batch size");
  Expect(result.outputs[0].find("hello") != std::string::npos,
         "simulator output should include prompt");
}

void TestTinyTextModelBackendReturnsStableInference() {
  TinyTextModelBackend backend(/*iterations=*/2);
  const auto requests = Requests({"urgent latency problem", "calm benchmark report"});

  BackendResult first = backend.ProcessBatch(requests, [] { return false; });
  BackendResult second = backend.ProcessBatch(requests, [] { return false; });

  Expect(first.code == BackendStatusCode::kOk, "tiny model should succeed");
  Expect(first.outputs.size() == requests.size(),
         "tiny model should return one output per input");
  Expect(first.outputs == second.outputs,
         "tiny model outputs should be deterministic for the same input");
  Expect(first.outputs[0].find("backend=tiny_text_model") != std::string::npos,
         "tiny model output should identify backend");
  Expect(first.outputs[0].find("label=") != std::string::npos,
         "tiny model output should include predicted label");
  Expect(first.outputs[0].find("confidence=") != std::string::npos,
         "tiny model output should include confidence");
}

void TestTinyTextModelHonorsCancellation() {
  TinyTextModelBackend backend(/*iterations=*/2);
  BackendResult result = backend.ProcessBatch(
      Requests({"cancel me"}), [] { return true; });

  Expect(result.code == BackendStatusCode::kCancelled,
         "tiny model should return cancelled status");
  Expect(result.outputs.empty(), "cancelled tiny model call should not return outputs");
}

void TestTinyLLMBackendReturnsStableGenerationMetadata() {
  TinyLLMBackend backend(/*max_generated_tokens=*/6, /*temperature=*/0.8f,
                         /*top_k=*/5);
  const auto requests =
      Requests({"Explain adaptive batching", "Trace an inference request"});

  BackendResult first = backend.ProcessBatch(requests, [] { return false; });
  BackendResult second = backend.ProcessBatch(requests, [] { return false; });

  Expect(first.code == BackendStatusCode::kOk, "tiny llm should succeed");
  Expect(first.outputs.size() == requests.size(),
         "tiny llm should return one output per input");
  Expect(first.outputs[0].find("backend=tiny_llm") != std::string::npos,
         "tiny llm output should identify backend");
  Expect(first.outputs[0].find("generated='") != std::string::npos,
         "tiny llm output should include generated text");
  Expect(first.outputs[0].find("prompt_tokens=") != std::string::npos,
         "tiny llm output should include prompt token count");
  Expect(first.outputs[0].find("generated_tokens=6") != std::string::npos,
         "tiny llm output should include generated token count");
  Expect(first.outputs[0].find("prefill_micros=") != std::string::npos,
         "tiny llm output should include prefill timing");
  Expect(first.outputs[0].find("decode_micros=") != std::string::npos,
         "tiny llm output should include decode timing");

  const auto first_generated =
      ExtractField(first.outputs[0], "generated='", "'");
  const auto second_generated =
      ExtractField(second.outputs[0], "generated='", "'");
  Expect(first_generated.has_value(), "tiny llm generated field should parse");
  Expect(first_generated == second_generated,
         "tiny llm generated text should be deterministic for same prompt");
}

void TestTinyLLMBackendHonorsCancellation() {
  TinyLLMBackend backend(/*max_generated_tokens=*/6, /*temperature=*/0.8f,
                         /*top_k=*/5);
  BackendResult result = backend.ProcessBatch(
      Requests({"cancel llm"}), [] { return true; });

  Expect(result.code == BackendStatusCode::kCancelled,
         "tiny llm should return cancelled status");
  Expect(result.outputs.empty(), "cancelled tiny llm call should not return outputs");
}

void TestTinyLLMConfigLoadsFromEnv() {
  setenv("LAMINAR_WORKER_BACKEND", "tiny_llm", 1);
  setenv("LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS", "7", 1);
  setenv("LAMINAR_TINY_LLM_TOP_K", "4", 1);
  setenv("LAMINAR_TINY_LLM_TEMPERATURE", "0.7", 1);

  BackendConfig config = BackendConfigFromEnv();

  Expect(config.kind == BackendKind::kTinyLLM,
         "tiny llm backend should load from env");
  Expect(config.tiny_llm_max_generated_tokens == 7,
         "tiny llm max generated tokens should load from env");
  Expect(config.tiny_llm_top_k == 4,
         "tiny llm top_k should load from env");
  Expect(config.tiny_llm_temperature > 0.69f &&
             config.tiny_llm_temperature < 0.71f,
         "tiny llm temperature should load from env");

  unsetenv("LAMINAR_WORKER_BACKEND");
  unsetenv("LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS");
  unsetenv("LAMINAR_TINY_LLM_TOP_K");
  unsetenv("LAMINAR_TINY_LLM_TEMPERATURE");
}

void TestContinuousTinyLLMBackendReturnsSchedulerMetadata() {
  BackendConfig config;
  config.kind = BackendKind::kContinuousTinyLLM;
  config.tiny_llm_max_generated_tokens = 4;
  config.tiny_llm_top_k = 5;
  config.tiny_llm_temperature = 0.8f;
  config.continuous_max_prefill_tokens_per_step = 8;
  config.continuous_max_decode_sequences_per_step = 2;
  config.continuous_kv_cache_blocks = 16;
  config.continuous_kv_block_tokens = 4;
  config.continuous_prefix_cache_tokens = 2;

  std::unique_ptr<InferenceBackend> backend = CreateBackend(config);
  BackendResult result = backend->ProcessBatch(
      Requests({"shared prefix alpha", "shared prefix beta"}),
      [] { return false; });

  Expect(result.code == BackendStatusCode::kOk,
         "continuous tiny llm backend should succeed");
  Expect(result.outputs.size() == 2,
         "continuous tiny llm should return one output per input");
  Expect(result.outputs[0].find("backend=continuous_tiny_llm") !=
             std::string::npos,
         "continuous tiny llm output should identify backend");
  Expect(result.output_metadata.size() == 2,
         "continuous tiny llm should return metadata per output");
  Expect(result.output_metadata[0]["scheduler.total_steps"] == "4",
         "metadata should include scheduler step count");
  Expect(result.output_metadata[0]["scheduler.decode_steps"] == "4",
         "metadata should include decode step count");
  Expect(result.output_metadata[0]["scheduler.average_decode_batch_size"] ==
             "2.00",
         "metadata should include decode batch utilization");
  Expect(result.output_metadata[0].count("kv_cache.high_watermark_blocks") == 1,
         "metadata should include kv-cache high watermark");
  Expect(result.output_metadata[0]["sequence.first_token_step"] == "0",
         "metadata should include per-sequence TTFT step proxy");
  Expect(result.output_metadata[1]["sequence.prefix_cache_hit"] == "true",
         "second shared-prefix sequence should hit the prefix cache");
}

void TestContinuousTinyLLMConfigLoadsFromEnv() {
  setenv("LAMINAR_WORKER_BACKEND", "continuous_tiny_llm", 1);
  setenv("LAMINAR_CONTINUOUS_MAX_PREFILL_TOKENS_PER_STEP", "11", 1);
  setenv("LAMINAR_CONTINUOUS_MAX_DECODE_SEQUENCES_PER_STEP", "3", 1);
  setenv("LAMINAR_CONTINUOUS_KV_CACHE_BLOCKS", "123", 1);
  setenv("LAMINAR_CONTINUOUS_KV_BLOCK_TOKENS", "7", 1);
  setenv("LAMINAR_CONTINUOUS_PREFIX_CACHE_TOKENS", "2", 1);

  BackendConfig config = BackendConfigFromEnv();

  Expect(config.kind == BackendKind::kContinuousTinyLLM,
         "continuous tiny llm backend should load from env");
  Expect(config.continuous_max_prefill_tokens_per_step == 11,
         "continuous prefill budget should load from env");
  Expect(config.continuous_max_decode_sequences_per_step == 3,
         "continuous decode width should load from env");
  Expect(config.continuous_kv_cache_blocks == 123,
         "continuous kv block count should load from env");
  Expect(config.continuous_kv_block_tokens == 7,
         "continuous kv block size should load from env");
  Expect(config.continuous_prefix_cache_tokens == 2,
         "continuous prefix cache tokens should load from env");

  unsetenv("LAMINAR_WORKER_BACKEND");
  unsetenv("LAMINAR_CONTINUOUS_MAX_PREFILL_TOKENS_PER_STEP");
  unsetenv("LAMINAR_CONTINUOUS_MAX_DECODE_SEQUENCES_PER_STEP");
  unsetenv("LAMINAR_CONTINUOUS_KV_CACHE_BLOCKS");
  unsetenv("LAMINAR_CONTINUOUS_KV_BLOCK_TOKENS");
  unsetenv("LAMINAR_CONTINUOUS_PREFIX_CACHE_TOKENS");
}

void TestLlamaCppBackendRequiresModelPath() {
  LlamaCppBackend backend(/*binary_path=*/"llama-cli", /*model_path=*/"",
                          /*max_tokens=*/8, /*context_size=*/256,
                          /*threads=*/0, /*temperature=*/0.2f,
                          /*timeout_ms=*/1000);
  BackendResult result = backend.ProcessBatch(
      Requests({"missing model"}), [] { return false; });

  Expect(result.code == BackendStatusCode::kUnavailable,
         "llama.cpp backend should report unavailable without a model path");
  Expect(result.message.find("LAMINAR_LLAMA_CPP_MODEL") != std::string::npos,
         "llama.cpp missing model message should mention model env var");
}

void TestLlamaCppBackendRunsConfiguredExecutable() {
  const auto dir = TestTempDir("llama-cpp");
  const auto binary = WriteFakeLlamaCli(dir);
  const auto model = dir / "tiny.gguf";
  {
    std::ofstream model_file(model);
    model_file << "fake model";
  }

  LlamaCppBackend backend(binary.string(), model.string(), /*max_tokens=*/11,
                          /*context_size=*/256, /*threads=*/2,
                          /*temperature=*/0.2f, /*timeout_ms=*/3000);

  BackendResult result = backend.ProcessBatch(
      Requests({"Explain adaptive batching"}), [] { return false; });

  Expect(result.code == BackendStatusCode::kOk,
         "llama.cpp backend should succeed with fake executable");
  Expect(result.outputs.size() == 1,
         "llama.cpp backend should return one output per input");
  Expect(result.outputs[0].find("backend=llama_cpp") != std::string::npos,
         "llama.cpp output should identify backend");
  Expect(result.outputs[0].find("fake llama completion for Explain adaptive batching") !=
             std::string::npos,
         "llama.cpp output should include executable output");
  Expect(result.outputs[0].find("model='tiny.gguf'") != std::string::npos,
         "llama.cpp output should include model basename");
  Expect(result.outputs[0].find("max_tokens=11") != std::string::npos,
         "llama.cpp output should include max token config");

  std::filesystem::remove_all(dir);
}

void TestLlamaCppBackendHonorsInitialCancellation() {
  LlamaCppBackend backend(/*binary_path=*/"llama-cli",
                          /*model_path=*/"/tmp/missing.gguf",
                          /*max_tokens=*/8, /*context_size=*/256,
                          /*threads=*/0, /*temperature=*/0.2f,
                          /*timeout_ms=*/1000);
  BackendResult result = backend.ProcessBatch(
      Requests({"cancel llama"}), [] { return true; });

  Expect(result.code == BackendStatusCode::kCancelled,
         "llama.cpp backend should honor cancellation before process launch");
  Expect(result.outputs.empty(),
         "cancelled llama.cpp call should not return outputs");
}

void TestLlamaCppConfigLoadsFromEnv() {
  setenv("LAMINAR_WORKER_BACKEND", "llama_cpp", 1);
  setenv("LAMINAR_LLAMA_CPP_BINARY", "/tmp/fake-llama-cli", 1);
  setenv("LAMINAR_LLAMA_CPP_MODEL", "/tmp/model.gguf", 1);
  setenv("LAMINAR_LLAMA_CPP_MAX_TOKENS", "17", 1);
  setenv("LAMINAR_LLAMA_CPP_CONTEXT_SIZE", "1024", 1);
  setenv("LAMINAR_LLAMA_CPP_THREADS", "3", 1);
  setenv("LAMINAR_LLAMA_CPP_TEMPERATURE", "0.4", 1);
  setenv("LAMINAR_LLAMA_CPP_TIMEOUT_MS", "12345", 1);

  BackendConfig config = BackendConfigFromEnv();

  Expect(config.kind == BackendKind::kLlamaCpp,
         "llama.cpp backend should load from env");
  Expect(config.llama_cpp_binary_path == "/tmp/fake-llama-cli",
         "llama.cpp binary path should load from env");
  Expect(config.llama_cpp_model_path == "/tmp/model.gguf",
         "llama.cpp model path should load from env");
  Expect(config.llama_cpp_max_tokens == 17,
         "llama.cpp max tokens should load from env");
  Expect(config.llama_cpp_context_size == 1024,
         "llama.cpp context size should load from env");
  Expect(config.llama_cpp_threads == 3,
         "llama.cpp threads should load from env");
  Expect(config.llama_cpp_temperature > 0.39f &&
             config.llama_cpp_temperature < 0.41f,
         "llama.cpp temperature should load from env");
  Expect(config.llama_cpp_timeout_ms == 12345,
         "llama.cpp timeout should load from env");

  unsetenv("LAMINAR_WORKER_BACKEND");
  unsetenv("LAMINAR_LLAMA_CPP_BINARY");
  unsetenv("LAMINAR_LLAMA_CPP_MODEL");
  unsetenv("LAMINAR_LLAMA_CPP_MAX_TOKENS");
  unsetenv("LAMINAR_LLAMA_CPP_CONTEXT_SIZE");
  unsetenv("LAMINAR_LLAMA_CPP_THREADS");
  unsetenv("LAMINAR_LLAMA_CPP_TEMPERATURE");
  unsetenv("LAMINAR_LLAMA_CPP_TIMEOUT_MS");
}

void TestLlamaServerBackendCallsCompletionEndpoint() {
  FakeLlamaServer server(
      "data: {\"content\":\"server \",\"tokens_predicted\":2,\"tokens_evaluated\":7}\n\n"
      "data: {\"content\":\"completion\",\"tokens_predicted\":5,\"tokens_evaluated\":7}\n\n");
  LlamaServerBackend backend(server.Url(), /*max_tokens=*/13,
                             /*temperature=*/0.25f, /*timeout_ms=*/3000,
                             /*cache_prompt=*/true, /*stream=*/true,
                             /*timings_per_token=*/true);

  BackendResult result = backend.ProcessBatch(
      Requests({"Explain warmed llama serving"}), [] { return false; });

  Expect(result.code == BackendStatusCode::kOk,
         "llama server backend should succeed against fake server");
  Expect(result.outputs.size() == 1,
         "llama server backend should return one output per input");
  Expect(result.outputs[0].find("backend=llama_server") != std::string::npos,
         "llama server output should identify backend");
  Expect(result.outputs[0].find("server completion") != std::string::npos,
         "llama server output should include generated content");
  Expect(result.outputs[0].find("tokens_predicted=5") != std::string::npos,
         "llama server output should include predicted token count");
  Expect(result.outputs[0].find("tokens_evaluated=7") != std::string::npos,
         "llama server output should include evaluated token count");
  Expect(result.output_metadata.size() == 1,
         "llama server backend should return one metadata map per output");
  Expect(result.output_metadata[0]["stream"] == "true",
         "llama server metadata should identify streaming mode");
  Expect(result.output_metadata[0]["stream_chunks"] == "2",
         "llama server metadata should include SSE chunk count");
  Expect(result.output_metadata[0]["tokens_predicted"] == "5",
         "llama server metadata should include token count");
  Expect(result.output_metadata[0]["tokens_evaluated"] == "7",
         "llama server metadata should include prompt token count");
  Expect(result.output_metadata[0].count("ttft_micros") == 1,
         "llama server metadata should include first-token latency");

  const std::string request = server.LastRequest();
  Expect(request.find("POST /completion HTTP/1.1") != std::string::npos,
         "llama server backend should POST to /completion");
  Expect(request.find("\"prompt\":\"Explain warmed llama serving\"") !=
             std::string::npos,
         "llama server request should include prompt JSON");
  Expect(request.find("\"n_predict\":13") != std::string::npos,
         "llama server request should include token budget");
  Expect(request.find("\"cache_prompt\":true") != std::string::npos,
         "llama server request should request prompt cache reuse");
  Expect(request.find("\"stream\":true") != std::string::npos,
         "llama server request should enable streaming for warmed path");
  Expect(request.find("\"timings_per_token\":true") != std::string::npos,
         "llama server request should request per-token timing metadata");
  Expect(request.find("\"return_tokens\":true") != std::string::npos,
         "llama server request should request token metadata");
}

void TestLlamaServerBackendStreamsCompletionEvents() {
  FakeLlamaServer server(
      "data: {\"content\":\"stream \",\"tokens_predicted\":1,\"tokens_evaluated\":4}\n\n"
      "data: {\"content\":\"tokens\",\"tokens_predicted\":2,\"tokens_evaluated\":4}\n\n"
      "data: [DONE]\n\n");
  LlamaServerBackend backend(server.Url(), /*max_tokens=*/8,
                             /*temperature=*/0.25f, /*timeout_ms=*/3000,
                             /*cache_prompt=*/true, /*stream=*/true,
                             /*timings_per_token=*/true);

  std::vector<BackendStreamEvent> events;
  BackendRequest request;
  request.id = "req-stream";
  request.prompt = "stream this prompt";
  request.trace_id = "trace-stream";
  BackendResult result = backend.ProcessStream(
      request, [] { return false; },
      [&events](const BackendStreamEvent& event) {
        events.push_back(event);
        return true;
      });

  Expect(result.code == BackendStatusCode::kOk,
         "llama server streaming should complete successfully");
  Expect(events.size() == 3,
         "llama server streaming should emit two tokens and one done event");
  Expect(events[0].event_type == "token" && events[0].delta == "stream ",
         "first streaming event should include first token delta");
  Expect(events[1].event_type == "token" && events[1].delta == "tokens",
         "second streaming event should include second token delta");
  Expect(events[2].event_type == "done",
         "last streaming event should be done");
  Expect(events[2].result.find("stream tokens") != std::string::npos,
         "done streaming event should include full generated result");
  Expect(events[2].metadata["stream_chunks"] == "2",
         "done streaming metadata should include stream chunk count");
  Expect(events[2].metadata["tokens_predicted"] == "2",
         "done streaming metadata should include final token count");
}

void TestLlamaServerBackendHonorsInitialCancellation() {
  LlamaServerBackend backend(/*server_url=*/"http://127.0.0.1:1/completion",
                             /*max_tokens=*/8, /*temperature=*/0.2f,
                             /*timeout_ms=*/1000, /*cache_prompt=*/true,
                             /*stream=*/true, /*timings_per_token=*/true);
  BackendResult result = backend.ProcessBatch(
      Requests({"cancel server"}), [] { return true; });

  Expect(result.code == BackendStatusCode::kCancelled,
         "llama server backend should honor cancellation before request");
  Expect(result.outputs.empty(),
         "cancelled llama server call should not return outputs");
}

void TestLlamaServerConfigLoadsFromEnv() {
  setenv("LAMINAR_WORKER_BACKEND", "llama_server", 1);
  setenv("LAMINAR_LLAMA_SERVER_URL", "http://127.0.0.1:8081/completion", 1);
  setenv("LAMINAR_LLAMA_SERVER_MAX_TOKENS", "19", 1);
  setenv("LAMINAR_LLAMA_SERVER_TEMPERATURE", "0.3", 1);
  setenv("LAMINAR_LLAMA_SERVER_TIMEOUT_MS", "2345", 1);
  setenv("LAMINAR_LLAMA_SERVER_CACHE_PROMPT", "false", 1);
  setenv("LAMINAR_LLAMA_SERVER_STREAM", "false", 1);
  setenv("LAMINAR_LLAMA_SERVER_TIMINGS_PER_TOKEN", "false", 1);

  BackendConfig config = BackendConfigFromEnv();

  Expect(config.kind == BackendKind::kLlamaServer,
         "llama server backend should load from env");
  Expect(config.llama_server_url == "http://127.0.0.1:8081/completion",
         "llama server URL should load from env");
  Expect(config.llama_server_max_tokens == 19,
         "llama server max tokens should load from env");
  Expect(config.llama_server_temperature > 0.29f &&
             config.llama_server_temperature < 0.31f,
         "llama server temperature should load from env");
  Expect(config.llama_server_timeout_ms == 2345,
         "llama server timeout should load from env");
  Expect(!config.llama_server_cache_prompt,
         "llama server cache_prompt should load from env");
  Expect(!config.llama_server_stream,
         "llama server stream flag should load from env");
  Expect(!config.llama_server_timings_per_token,
         "llama server timings flag should load from env");

  unsetenv("LAMINAR_WORKER_BACKEND");
  unsetenv("LAMINAR_LLAMA_SERVER_URL");
  unsetenv("LAMINAR_LLAMA_SERVER_MAX_TOKENS");
  unsetenv("LAMINAR_LLAMA_SERVER_TEMPERATURE");
  unsetenv("LAMINAR_LLAMA_SERVER_TIMEOUT_MS");
  unsetenv("LAMINAR_LLAMA_SERVER_CACHE_PROMPT");
  unsetenv("LAMINAR_LLAMA_SERVER_STREAM");
  unsetenv("LAMINAR_LLAMA_SERVER_TIMINGS_PER_TOKEN");
}

}  // namespace

int main() {
  TestBackendModeParsing();
  TestOnnxRuntimeBackendIsExplicitlyConfigured();
  TestSimulatorBackendIsDeterministic();
  TestTinyTextModelBackendReturnsStableInference();
  TestTinyTextModelHonorsCancellation();
  TestTinyLLMBackendReturnsStableGenerationMetadata();
  TestTinyLLMBackendHonorsCancellation();
  TestTinyLLMConfigLoadsFromEnv();
  TestContinuousTinyLLMBackendReturnsSchedulerMetadata();
  TestContinuousTinyLLMConfigLoadsFromEnv();
  TestLlamaCppBackendRequiresModelPath();
  TestLlamaCppBackendRunsConfiguredExecutable();
  TestLlamaCppBackendHonorsInitialCancellation();
  TestLlamaCppConfigLoadsFromEnv();
  TestLlamaServerBackendCallsCompletionEndpoint();
  TestLlamaServerBackendStreamsCompletionEvents();
  TestLlamaServerBackendHonorsInitialCancellation();
  TestLlamaServerConfigLoadsFromEnv();

  if (failures != 0) {
    std::cerr << failures << " backend test failure(s)" << std::endl;
    return EXIT_FAILURE;
  }
  std::cout << "backend tests passed" << std::endl;
  return EXIT_SUCCESS;
}
