#include <grpcpp/grpcpp.h>
#include <grpcpp/health_check_service_interface.h>
#include <grpcpp/server_builder.h>
#include <algorithm>
#include <chrono>
#include <iostream>
#include <memory>
#include <string>
#include <thread>

#include "proto/inference.grpc.pb.h"
#include "proto/inference.pb.h"

using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
using grpc::Status;
using grpc::StatusCode;
using inference::BatchRequest;
using inference::BatchResponse;
using inference::InferenceService;
using inference::ResponseItem;

// InferenceServiceImpl simulates GPU processing with dynamic batch handling
class InferenceServiceImpl final : public InferenceService::Service {
 public:
  Status ProcessBatch(ServerContext* context, const BatchRequest* request,
                      BatchResponse* response) override {
    const int batch_size = request->requests_size();
    
    // Log batch size for debugging and analysis
    std::cout << "[C++ Worker] Processing batch of size: " << batch_size 
              << std::endl;

    // Simulate GPU processing time: 20ms base + 5ms per request
    // This models real GPU behavior where larger batches take longer
    // but achieve better throughput per request
    const int base_latency_ms = 20;
    const int per_request_latency_ms = 5;
    const int total_latency_ms = base_latency_ms + (batch_size * per_request_latency_ms);
    
    // Sleep in short chunks so client cancellation is observed promptly.
    int remaining_latency_ms = total_latency_ms;
    while (remaining_latency_ms > 0) {
      if (context->IsCancelled()) {
        std::cout << "[C++ Worker] Batch cancelled by client" << std::endl;
        return Status(StatusCode::CANCELLED, "batch cancelled");
      }

      const int step_ms = std::min(remaining_latency_ms, 5);
      std::this_thread::sleep_for(std::chrono::milliseconds(step_ms));
      remaining_latency_ms -= step_ms;
    }

    // Process each request in the batch
    for (const auto& req : request->requests()) {
      ResponseItem* response_item = response->add_responses();
      response_item->set_id(req.id());
      response_item->set_trace_id(req.trace_id());
      
      // Simulate inference result (in real system, this would be model output)
      response_item->set_result(
          "Processed: '" + req.prompt() + "' (batch_size=" + 
          std::to_string(batch_size) + ", latency=" + 
          std::to_string(total_latency_ms) + "ms)");
    }

    std::cout << "[C++ Worker] Completed batch in " << total_latency_ms 
              << "ms" << std::endl;

    return Status::OK;
  }
};

// RunServer starts the gRPC server on the specified port
void RunServer(const std::string& server_address) {
  InferenceServiceImpl service;

  grpc::EnableDefaultHealthCheckService(true);

  ServerBuilder builder;
  // Configure server to listen on the specified address
  builder.AddListeningPort(server_address, grpc::InsecureServerCredentials());
  builder.RegisterService(&service);

  // Build and start the server
  std::unique_ptr<Server> server(builder.BuildAndStart());
  std::cout << "[C++ Worker] Server listening on " << server_address 
            << std::endl;
  std::cout << "[C++ Worker] Ready to process batches..." << std::endl;

  // Wait for the server to shutdown (triggered by Ctrl+C or kill signal)
  server->Wait();
}

int main(int argc, char** argv) {
  // Default server address
  std::string server_address = "0.0.0.0:50051";
  
  // Allow override via command line argument
  if (argc > 1) {
    server_address = argv[1];
  }

  std::cout << "=== Laminar Inference - C++ Worker ===" 
            << std::endl;
  std::cout << "Simulating deterministic batch processing" << std::endl;
  std::cout << "Processing model: Base 20ms + 5ms per request" << std::endl;
  std::cout << "========================================" << std::endl;

  RunServer(server_address);

  return 0;
}
