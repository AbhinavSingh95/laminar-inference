#include <grpcpp/grpcpp.h>
#include <grpcpp/health_check_service_interface.h>
#include <grpcpp/server_builder.h>
#include <chrono>
#include <iostream>
#include <memory>
#include <string>
#include <utility>
#include <vector>

#include "backend/inference_backend.h"
#include "proto/inference.grpc.pb.h"
#include "proto/inference.pb.h"

using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
using grpc::ServerWriter;
using grpc::Status;
using grpc::StatusCode;
using inference::BatchRequest;
using inference::BatchResponse;
using inference::InferenceService;
using inference::ResponseItem;
using inference::StreamResponse;

class InferenceServiceImpl final : public InferenceService::Service {
 public:
  explicit InferenceServiceImpl(std::unique_ptr<InferenceBackend> backend)
      : backend_(std::move(backend)) {}

  Status ProcessBatch(ServerContext* context, const BatchRequest* request,
                      BatchResponse* response) override {
    const int batch_size = request->requests_size();

    std::cout << "[C++ Worker] Processing batch of size: " << batch_size
              << " with backend=" << backend_->Name()
              << std::endl;

    std::vector<BackendRequest> backend_requests;
    backend_requests.reserve(request->requests_size());
    for (const auto& req : request->requests()) {
      BackendRequest backend_request;
      backend_request.id = req.id();
      backend_request.prompt = req.prompt();
      backend_request.trace_id = req.trace_id();
      backend_requests.push_back(backend_request);
    }

    const auto started = std::chrono::steady_clock::now();
    BackendResult backend_result = backend_->ProcessBatch(
        backend_requests, [context]() { return context->IsCancelled(); });
    const auto elapsed = std::chrono::steady_clock::now() - started;
    const auto elapsed_ms =
        std::chrono::duration_cast<std::chrono::milliseconds>(elapsed).count();
    const auto elapsed_micros =
        std::chrono::duration_cast<std::chrono::microseconds>(elapsed).count();

    if (backend_result.code == BackendStatusCode::kCancelled) {
      std::cout << "[C++ Worker] Batch cancelled by client" << std::endl;
      return Status(StatusCode::CANCELLED, backend_result.message);
    }
    if (backend_result.code == BackendStatusCode::kInvalidArgument) {
      return Status(StatusCode::INVALID_ARGUMENT, backend_result.message);
    }
    if (backend_result.code == BackendStatusCode::kUnavailable) {
      return Status(StatusCode::UNAVAILABLE, backend_result.message);
    }
    if (backend_result.outputs.size() != backend_requests.size()) {
      return Status(StatusCode::INTERNAL,
                    "backend returned the wrong number of outputs");
    }
    if (!backend_result.output_metadata.empty() &&
        backend_result.output_metadata.size() != backend_requests.size()) {
      return Status(StatusCode::INTERNAL,
                    "backend returned the wrong number of metadata records");
    }

    for (int i = 0; i < request->requests_size(); ++i) {
      const auto& req = request->requests(i);
      ResponseItem* response_item = response->add_responses();
      response_item->set_id(req.id());
      response_item->set_trace_id(req.trace_id());
      response_item->set_result(backend_result.outputs[i]);
      response_item->set_backend_name(backend_->Name());
      response_item->set_backend_latency_micros(elapsed_micros);
      if (!backend_result.output_metadata.empty()) {
        auto* metadata = response_item->mutable_backend_metadata();
        for (const auto& [key, value] : backend_result.output_metadata[i]) {
          (*metadata)[key] = value;
        }
      }
    }

    std::cout << "[C++ Worker] Completed batch in " << elapsed_ms
              << "ms" << std::endl;

    return Status::OK;
  }

  Status Stream(ServerContext* context, const inference::RequestItem* request,
                ServerWriter<StreamResponse>* writer) override {
    BackendRequest backend_request;
    backend_request.id = request->id();
    backend_request.prompt = request->prompt();
    backend_request.trace_id = request->trace_id();

    const auto started = std::chrono::steady_clock::now();
    BackendResult backend_result = backend_->ProcessStream(
        backend_request, [context]() { return context->IsCancelled(); },
        [&](const BackendStreamEvent& event) {
          StreamResponse response;
          response.set_id(request->id());
          response.set_trace_id(request->trace_id());
          response.set_event_type(event.event_type);
          response.set_delta(event.delta);
          response.set_result(event.result);
          response.set_backend_name(backend_->Name());
          for (const auto& [key, value] : event.metadata) {
            (*response.mutable_backend_metadata())[key] = value;
          }
          return writer->Write(response);
        });
    const auto elapsed = std::chrono::steady_clock::now() - started;
    const auto elapsed_micros =
        std::chrono::duration_cast<std::chrono::microseconds>(elapsed).count();

    if (backend_result.code == BackendStatusCode::kCancelled) {
      return Status(StatusCode::CANCELLED, backend_result.message);
    }
    if (backend_result.code == BackendStatusCode::kInvalidArgument) {
      return Status(StatusCode::INVALID_ARGUMENT, backend_result.message);
    }
    if (backend_result.code == BackendStatusCode::kUnavailable) {
      return Status(StatusCode::UNAVAILABLE, backend_result.message);
    }
    if (backend_result.outputs.size() != 1) {
      return Status(StatusCode::INTERNAL,
                    "backend returned the wrong number of stream outputs");
    }

    (void)elapsed_micros;
    return Status::OK;
  }

 private:
  std::unique_ptr<InferenceBackend> backend_;
};

void RunServer(const std::string& server_address,
               std::unique_ptr<InferenceBackend> backend) {
  InferenceServiceImpl service(std::move(backend));

  grpc::EnableDefaultHealthCheckService(true);

  ServerBuilder builder;
  builder.AddListeningPort(server_address, grpc::InsecureServerCredentials());
  builder.RegisterService(&service);

  std::unique_ptr<Server> server(builder.BuildAndStart());
  std::cout << "[C++ Worker] Server listening on " << server_address
            << std::endl;
  std::cout << "[C++ Worker] Ready to process batches..." << std::endl;

  server->Wait();
}

int main(int argc, char** argv) {
  std::string server_address = "0.0.0.0:50051";
  if (argc > 1) {
    server_address = argv[1];
  }

  BackendConfig backend_config = BackendConfigFromEnv();
  std::unique_ptr<InferenceBackend> backend = CreateBackend(backend_config);

  std::cout << "=== Laminar Inference - C++ Worker ===" << std::endl;
  std::cout << "Backend: " << backend->Name() << std::endl;
  if (backend_config.kind == BackendKind::kSimulator) {
    std::cout << "Simulator latency model: base "
              << backend_config.simulator_base_latency_ms
              << "ms + "
              << backend_config.simulator_per_request_latency_ms
              << "ms per request" << std::endl;
  } else if (backend_config.kind == BackendKind::kTinyTextModel) {
    std::cout << "Tiny model iterations: "
              << backend_config.tiny_model_iterations << std::endl;
  } else if (backend_config.kind == BackendKind::kTinyLLM) {
    std::cout << "Tiny LLM max generated tokens: "
              << backend_config.tiny_llm_max_generated_tokens << std::endl;
    std::cout << "Tiny LLM top_k: "
              << backend_config.tiny_llm_top_k << std::endl;
    std::cout << "Tiny LLM temperature: "
              << backend_config.tiny_llm_temperature << std::endl;
  } else if (backend_config.kind == BackendKind::kContinuousTinyLLM) {
    std::cout << "Continuous Tiny LLM max generated tokens: "
              << backend_config.tiny_llm_max_generated_tokens << std::endl;
    std::cout << "Continuous Tiny LLM prefill tokens/step: "
              << backend_config.continuous_max_prefill_tokens_per_step
              << std::endl;
    std::cout << "Continuous Tiny LLM decode sequences/step: "
              << backend_config.continuous_max_decode_sequences_per_step
              << std::endl;
    std::cout << "Continuous Tiny LLM KV blocks: "
              << backend_config.continuous_kv_cache_blocks << std::endl;
    std::cout << "Continuous Tiny LLM KV block tokens: "
              << backend_config.continuous_kv_block_tokens << std::endl;
    std::cout << "Continuous Tiny LLM prefix cache tokens: "
              << backend_config.continuous_prefix_cache_tokens << std::endl;
  } else if (backend_config.kind == BackendKind::kLlamaCpp) {
    std::cout << "llama.cpp binary: "
              << backend_config.llama_cpp_binary_path << std::endl;
    std::cout << "llama.cpp model: "
              << backend_config.llama_cpp_model_path << std::endl;
    std::cout << "llama.cpp max tokens: "
              << backend_config.llama_cpp_max_tokens << std::endl;
    std::cout << "llama.cpp context size: "
              << backend_config.llama_cpp_context_size << std::endl;
    std::cout << "llama.cpp threads: "
              << backend_config.llama_cpp_threads << std::endl;
    std::cout << "llama.cpp temperature: "
              << backend_config.llama_cpp_temperature << std::endl;
    std::cout << "llama.cpp timeout ms: "
              << backend_config.llama_cpp_timeout_ms << std::endl;
  } else if (backend_config.kind == BackendKind::kLlamaServer) {
    std::cout << "llama.cpp server URL: "
              << backend_config.llama_server_url << std::endl;
    std::cout << "llama.cpp server max tokens: "
              << backend_config.llama_server_max_tokens << std::endl;
    std::cout << "llama.cpp server temperature: "
              << backend_config.llama_server_temperature << std::endl;
    std::cout << "llama.cpp server timeout ms: "
              << backend_config.llama_server_timeout_ms << std::endl;
    std::cout << "llama.cpp server cache_prompt: "
              << (backend_config.llama_server_cache_prompt ? "true" : "false")
              << std::endl;
    std::cout << "llama.cpp server stream: "
              << (backend_config.llama_server_stream ? "true" : "false")
              << std::endl;
    std::cout << "llama.cpp server timings_per_token: "
              << (backend_config.llama_server_timings_per_token ? "true"
                                                                 : "false")
              << std::endl;
  } else if (backend_config.kind == BackendKind::kOnnxRuntime) {
    std::cout << "ONNX model path: " << backend_config.onnx_model_path
              << std::endl;
    std::cout << "ONNX Runtime library: "
              << backend_config.onnx_runtime_library_path << std::endl;
  }
  std::cout << "========================================" << std::endl;

  RunServer(server_address, std::move(backend));

  return 0;
}
