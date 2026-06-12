#ifndef BACKEND_SCHEDULING_CONTINUOUS_BATCHER_H_
#define BACKEND_SCHEDULING_CONTINUOUS_BATCHER_H_

#include <functional>
#include <memory>
#include <string>
#include <vector>

namespace laminar::backend::scheduling {

struct KvCacheConfig {
  int total_blocks = 1024;
  int tokens_per_block = 16;
};

struct KvCacheStats {
  int total_blocks = 0;
  int used_blocks = 0;
  int free_blocks = 0;
  int high_watermark_blocks = 0;
  int active_sequences = 0;
  int cached_prefixes = 0;
  int prefix_cache_hits = 0;
  int prefix_cache_misses = 0;
  int evictions = 0;
  int allocation_failures = 0;
};

struct KvAllocation {
  bool ok = false;
  bool cache_hit = false;
  int attached_blocks = 0;
  std::string message;
};

class KvBlockAllocator {
 public:
  explicit KvBlockAllocator(KvCacheConfig config);

  KvAllocation AllocatePrompt(const std::string& sequence_id,
                              int prompt_tokens,
                              const std::string& prefix_cache_key,
                              int prefix_tokens = -1);
  KvAllocation AppendTokens(const std::string& sequence_id, int token_count);
  void ReleaseSequence(const std::string& sequence_id);

  KvCacheStats Stats() const;
  int SequenceBlockCount(const std::string& sequence_id) const;

 private:
  struct Impl;
  std::shared_ptr<Impl> impl_;
};

struct ContinuousBatcherConfig {
  int max_prefill_tokens_per_step = 64;
  int max_decode_sequences_per_step = 8;
  int kv_cache_blocks = 1024;
  int kv_block_tokens = 16;
};

struct SequenceRequest {
  std::string id;
  int prompt_tokens = 0;
  int max_generated_tokens = 0;
  int priority = 0;
  std::string prefix_cache_key;
  int prefix_cache_tokens = -1;
};

enum class SequenceStatus {
  kCompleted,
  kRejected,
  kCancelled,
};

struct SequenceResult {
  std::string id;
  SequenceStatus status = SequenceStatus::kRejected;
  int prompt_tokens = 0;
  int generated_tokens = 0;
  int first_token_step = -1;
  int completion_step = -1;
  int kv_blocks = 0;
  bool prefix_cache_hit = false;
  std::string message;
};

struct SchedulerStep {
  int step_index = 0;
  int prefill_tokens = 0;
  int decoded_tokens = 0;
  int queued_sequences = 0;
  int active_sequences = 0;
  int kv_blocks_used = 0;
  std::vector<std::string> decoded_sequence_ids;
};

struct ContinuousBatchingStats {
  int total_steps = 0;
  int prefill_steps = 0;
  int decode_steps = 0;
  int total_prompt_tokens = 0;
  int total_generated_tokens = 0;
  double average_decode_batch_size = 0.0;
  KvCacheStats kv_cache;
};

struct ContinuousBatchingRun {
  std::vector<SequenceResult> results;
  std::vector<SchedulerStep> steps;
  ContinuousBatchingStats stats;
};

class ContinuousBatcher {
 public:
  explicit ContinuousBatcher(ContinuousBatcherConfig config);

  ContinuousBatchingRun Run(
      const std::vector<SequenceRequest>& requests,
      const std::function<bool()>& is_cancelled = [] { return false; });

 private:
  ContinuousBatcherConfig config_;
};

std::string SequenceStatusName(SequenceStatus status);

}  // namespace laminar::backend::scheduling

#endif  // BACKEND_SCHEDULING_CONTINUOUS_BATCHER_H_
