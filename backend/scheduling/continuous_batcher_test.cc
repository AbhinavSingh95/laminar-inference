#include "backend/scheduling/continuous_batcher.h"

#include <cstdlib>
#include <iostream>
#include <string>
#include <vector>

namespace {

using laminar::backend::scheduling::ContinuousBatcher;
using laminar::backend::scheduling::ContinuousBatcherConfig;
using laminar::backend::scheduling::KvBlockAllocator;
using laminar::backend::scheduling::KvCacheConfig;
using laminar::backend::scheduling::SequenceRequest;
using laminar::backend::scheduling::SequenceStatus;

void Expect(bool condition, const std::string& message) {
  if (!condition) {
    std::cerr << "EXPECTATION FAILED: " << message << std::endl;
    std::exit(1);
  }
}

SequenceRequest Request(std::string id, int prompt_tokens,
                        int max_generated_tokens) {
  SequenceRequest request;
  request.id = std::move(id);
  request.prompt_tokens = prompt_tokens;
  request.max_generated_tokens = max_generated_tokens;
  return request;
}

void TestContinuousBatcherInterleavesPrefillAndDecode() {
  ContinuousBatcherConfig config;
  config.max_prefill_tokens_per_step = 4;
  config.max_decode_sequences_per_step = 2;
  config.kv_cache_blocks = 32;
  config.kv_block_tokens = 4;

  ContinuousBatcher batcher(config);
  const auto run = batcher.Run({
      Request("a", /*prompt_tokens=*/4, /*max_generated_tokens=*/3),
      Request("b", /*prompt_tokens=*/4, /*max_generated_tokens=*/2),
  });

  Expect(run.results.size() == 2, "scheduler should return one result per request");
  Expect(run.results[0].status == SequenceStatus::kCompleted,
         "first sequence should complete");
  Expect(run.results[1].status == SequenceStatus::kCompleted,
         "second sequence should complete");
  Expect(run.results[0].generated_tokens == 3,
         "first sequence should generate requested tokens");
  Expect(run.results[1].generated_tokens == 2,
         "second sequence should generate requested tokens");
  Expect(run.results[0].first_token_step == 0,
         "first sequence should decode in the first scheduler step");
  Expect(run.results[1].first_token_step == 1,
         "second sequence should join decoding after its prefill chunk");

  Expect(run.steps.size() == 3, "run should finish in three scheduler steps");
  Expect(run.steps[0].prefill_tokens == 4,
         "step 0 should consume one prefill chunk");
  Expect(run.steps[0].decoded_tokens == 1,
         "step 0 should decode the already-prefilled sequence");
  Expect(run.steps[1].prefill_tokens == 4,
         "step 1 should prefill the second sequence");
  Expect(run.steps[1].decoded_tokens == 2,
         "step 1 should decode both active sequences");
  Expect(run.steps[2].decoded_tokens == 2,
         "step 2 should finish both active sequences");
  Expect(run.stats.total_generated_tokens == 5,
         "stats should count generated tokens");
  Expect(run.stats.decode_steps == 3,
         "stats should count decode-bearing scheduler steps");
  Expect(run.stats.average_decode_batch_size > 1.66 &&
             run.stats.average_decode_batch_size < 1.67,
         "stats should expose average decode batch size");
}

void TestKvCacheRetainsSharesAndEvictsPrefixBlocks() {
  KvCacheConfig config;
  config.total_blocks = 3;
  config.tokens_per_block = 4;
  KvBlockAllocator allocator(config);

  auto first = allocator.AllocatePrompt("a", 8, "system-prefix");
  Expect(first.ok, "first prefix allocation should fit");
  Expect(!first.cache_hit, "first prefix allocation should be a cache miss");
  Expect(allocator.Stats().used_blocks == 2,
         "two prompt blocks should be resident");

  allocator.ReleaseSequence("a");
  Expect(allocator.Stats().used_blocks == 2,
         "released prefix blocks should remain cached");
  Expect(allocator.Stats().cached_prefixes == 1,
         "prefix cache should retain one entry");

  auto second = allocator.AllocatePrompt("b", 8, "system-prefix");
  Expect(second.ok, "second prefix allocation should fit");
  Expect(second.cache_hit, "second allocation should share cached prefix blocks");
  Expect(allocator.Stats().prefix_cache_hits == 1,
         "prefix cache hit should be counted");
  Expect(allocator.SequenceBlockCount("b") == 2,
         "shared sequence should attach the cached prompt blocks");

  auto append = allocator.AppendTokens("b", 1);
  Expect(append.ok, "decode token should allocate a generated-token block");
  Expect(allocator.Stats().used_blocks == 3,
         "generated token block should increase resident block count");

  allocator.ReleaseSequence("b");
  Expect(allocator.Stats().used_blocks == 2,
         "generated block should be freed while prefix remains cached");

  auto pressure = allocator.AllocatePrompt("c", 8, "");
  Expect(pressure.ok, "allocator should evict idle prefix cache under pressure");
  Expect(allocator.Stats().cached_prefixes == 0,
         "prefix cache should be empty after eviction");
  Expect(allocator.Stats().evictions == 1,
         "prefix cache eviction should be counted");
}

}  // namespace

int main() {
  TestContinuousBatcherInterleavesPrefillAndDecode();
  TestKvCacheRetainsSharesAndEvictsPrefixBlocks();
  std::cout << "continuous_batcher_test passed" << std::endl;
  return 0;
}
