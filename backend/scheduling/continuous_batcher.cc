#include "backend/scheduling/continuous_batcher.h"

#include <algorithm>
#include <map>
#include <numeric>
#include <set>
#include <utility>

namespace laminar::backend::scheduling {
namespace {

int ClampPositive(int value, int fallback) {
  return value > 0 ? value : fallback;
}

int CeilDiv(int value, int divisor) {
  if (value <= 0) {
    return 0;
  }
  return (value + divisor - 1) / divisor;
}

KvAllocation AllocationError(std::string message) {
  KvAllocation allocation;
  allocation.ok = false;
  allocation.message = std::move(message);
  return allocation;
}

KvAllocation AllocationOk(int attached_blocks, bool cache_hit = false) {
  KvAllocation allocation;
  allocation.ok = true;
  allocation.cache_hit = cache_hit;
  allocation.attached_blocks = attached_blocks;
  return allocation;
}

}  // namespace

struct KvBlockAllocator::Impl {
  struct Block {
    bool occupied = false;
    int ref_count = 0;
    std::string cached_prefix_key;
  };

  struct Sequence {
    int prompt_tokens = 0;
    int generated_tokens = 0;
    int prefix_block_count = 0;
    std::vector<int> block_ids;
  };

  struct PrefixEntry {
    int token_count = 0;
    std::vector<int> block_ids;
  };

  explicit Impl(KvCacheConfig input_config) {
    config.total_blocks = ClampPositive(input_config.total_blocks, 1);
    config.tokens_per_block = ClampPositive(input_config.tokens_per_block, 1);
    blocks.resize(config.total_blocks);
  }

  int FreeBlockCount() const {
    return static_cast<int>(std::count_if(
        blocks.begin(), blocks.end(),
        [](const Block& block) { return !block.occupied; }));
  }

  int UsedBlockCount() const {
    return static_cast<int>(blocks.size()) - FreeBlockCount();
  }

  void RefreshHighWatermark() {
    stats.high_watermark_blocks =
        std::max(stats.high_watermark_blocks, UsedBlockCount());
  }

  bool PrefixEntryIsIdle(const PrefixEntry& entry) const {
    for (int block_id : entry.block_ids) {
      if (block_id < 0 || block_id >= static_cast<int>(blocks.size())) {
        return false;
      }
      if (blocks[block_id].ref_count != 0) {
        return false;
      }
    }
    return true;
  }

  void FreeBlock(int block_id) {
    if (block_id < 0 || block_id >= static_cast<int>(blocks.size())) {
      return;
    }
    blocks[block_id] = Block{};
  }

  void EvictIdlePrefixesUntil(int required_free_blocks) {
    for (auto it = prefix_cache.begin();
         it != prefix_cache.end() && FreeBlockCount() < required_free_blocks;) {
      if (!PrefixEntryIsIdle(it->second)) {
        ++it;
        continue;
      }
      for (int block_id : it->second.block_ids) {
        FreeBlock(block_id);
      }
      it = prefix_cache.erase(it);
      ++stats.evictions;
    }
  }

  bool EnsureFreeBlocks(int block_count) {
    if (block_count <= FreeBlockCount()) {
      return true;
    }
    EvictIdlePrefixesUntil(block_count);
    return block_count <= FreeBlockCount();
  }

  std::vector<int> AllocateBlocks(int block_count,
                                  const std::string& cached_prefix_key) {
    std::vector<int> allocated;
    if (block_count <= 0) {
      return allocated;
    }
    if (!EnsureFreeBlocks(block_count)) {
      ++stats.allocation_failures;
      return {};
    }
    allocated.reserve(block_count);
    for (int index = 0; index < static_cast<int>(blocks.size()) &&
                        static_cast<int>(allocated.size()) < block_count;
         ++index) {
      Block& block = blocks[index];
      if (block.occupied) {
        continue;
      }
      block.occupied = true;
      block.ref_count = 1;
      block.cached_prefix_key = cached_prefix_key;
      allocated.push_back(index);
    }
    RefreshHighWatermark();
    return allocated;
  }

  KvCacheConfig config;
  std::vector<Block> blocks;
  std::map<std::string, Sequence> sequences;
  std::map<std::string, PrefixEntry> prefix_cache;
  KvCacheStats stats;
};

KvBlockAllocator::KvBlockAllocator(KvCacheConfig config)
    : impl_(std::make_shared<Impl>(config)) {}

KvAllocation KvBlockAllocator::AllocatePrompt(
    const std::string& sequence_id, int prompt_tokens,
    const std::string& prefix_cache_key, int prefix_tokens) {
  if (sequence_id.empty()) {
    return AllocationError("sequence id is required");
  }
  if (impl_->sequences.count(sequence_id) != 0) {
    return AllocationError("sequence already exists");
  }

  prompt_tokens = std::max(0, prompt_tokens);
  int cacheable_tokens = 0;
  if (!prefix_cache_key.empty()) {
    cacheable_tokens =
        prefix_tokens < 0 ? prompt_tokens : std::min(prompt_tokens, prefix_tokens);
    cacheable_tokens = std::max(0, cacheable_tokens);
  }
  const int prefix_block_count =
      CeilDiv(cacheable_tokens, impl_->config.tokens_per_block);
  const int suffix_block_count =
      CeilDiv(prompt_tokens - cacheable_tokens, impl_->config.tokens_per_block);
  const int block_count = prefix_block_count + suffix_block_count;
  Impl::Sequence sequence;
  sequence.prompt_tokens = prompt_tokens;
  sequence.prefix_block_count = prefix_block_count;

  if (!prefix_cache_key.empty() && cacheable_tokens > 0) {
    auto cached = impl_->prefix_cache.find(prefix_cache_key);
    if (cached != impl_->prefix_cache.end() &&
        cached->second.token_count == cacheable_tokens) {
      for (int block_id : cached->second.block_ids) {
        ++impl_->blocks[block_id].ref_count;
      }
      sequence.block_ids = cached->second.block_ids;
      const std::vector<int> suffix_blocks =
          impl_->AllocateBlocks(suffix_block_count, /*cached_prefix_key=*/"");
      if (static_cast<int>(suffix_blocks.size()) != suffix_block_count) {
        for (int block_id : sequence.block_ids) {
          --impl_->blocks[block_id].ref_count;
        }
        return AllocationError("kv cache exhausted");
      }
      sequence.block_ids.insert(sequence.block_ids.end(), suffix_blocks.begin(),
                                suffix_blocks.end());
      impl_->sequences.emplace(sequence_id, std::move(sequence));
      ++impl_->stats.prefix_cache_hits;
      return AllocationOk(block_count, /*cache_hit=*/true);
    }
    ++impl_->stats.prefix_cache_misses;
  }

  const std::vector<int> prefix_blocks =
      impl_->AllocateBlocks(prefix_block_count, prefix_cache_key);
  if (static_cast<int>(prefix_blocks.size()) != prefix_block_count) {
    return AllocationError("kv cache exhausted");
  }
  const std::vector<int> suffix_blocks =
      impl_->AllocateBlocks(suffix_block_count, /*cached_prefix_key=*/"");
  if (static_cast<int>(suffix_blocks.size()) != suffix_block_count) {
    for (int block_id : prefix_blocks) {
      impl_->FreeBlock(block_id);
    }
    return AllocationError("kv cache exhausted");
  }

  sequence.block_ids = prefix_blocks;
  sequence.block_ids.insert(sequence.block_ids.end(), suffix_blocks.begin(),
                            suffix_blocks.end());
  if (!prefix_cache_key.empty() && cacheable_tokens > 0) {
    Impl::PrefixEntry entry;
    entry.token_count = cacheable_tokens;
    entry.block_ids = prefix_blocks;
    impl_->prefix_cache[prefix_cache_key] = std::move(entry);
  }
  impl_->sequences.emplace(sequence_id, std::move(sequence));
  return AllocationOk(block_count);
}

KvAllocation KvBlockAllocator::AppendTokens(const std::string& sequence_id,
                                            int token_count) {
  auto sequence_it = impl_->sequences.find(sequence_id);
  if (sequence_it == impl_->sequences.end()) {
    return AllocationError("sequence does not exist");
  }
  if (token_count <= 0) {
    return AllocationOk(/*attached_blocks=*/0);
  }

  Impl::Sequence& sequence = sequence_it->second;
  const int before_blocks =
      CeilDiv(sequence.generated_tokens, impl_->config.tokens_per_block);
  const int after_blocks = CeilDiv(sequence.generated_tokens + token_count,
                                  impl_->config.tokens_per_block);
  const int additional_blocks = after_blocks - before_blocks;
  const std::vector<int> blocks =
      impl_->AllocateBlocks(additional_blocks, /*cached_prefix_key=*/"");
  if (static_cast<int>(blocks.size()) != additional_blocks) {
    return AllocationError("kv cache exhausted");
  }

  sequence.generated_tokens += token_count;
  sequence.block_ids.insert(sequence.block_ids.end(), blocks.begin(),
                            blocks.end());
  return AllocationOk(additional_blocks);
}

void KvBlockAllocator::ReleaseSequence(const std::string& sequence_id) {
  auto sequence_it = impl_->sequences.find(sequence_id);
  if (sequence_it == impl_->sequences.end()) {
    return;
  }
  for (int block_id : sequence_it->second.block_ids) {
    if (block_id < 0 || block_id >= static_cast<int>(impl_->blocks.size())) {
      continue;
    }
    auto& block = impl_->blocks[block_id];
    if (block.ref_count > 0) {
      --block.ref_count;
    }
    if (block.ref_count == 0 && block.cached_prefix_key.empty()) {
      impl_->FreeBlock(block_id);
    }
  }
  impl_->sequences.erase(sequence_it);
}

KvCacheStats KvBlockAllocator::Stats() const {
  KvCacheStats snapshot = impl_->stats;
  snapshot.total_blocks = static_cast<int>(impl_->blocks.size());
  snapshot.used_blocks = impl_->UsedBlockCount();
  snapshot.free_blocks = impl_->FreeBlockCount();
  snapshot.active_sequences = static_cast<int>(impl_->sequences.size());
  snapshot.cached_prefixes = static_cast<int>(impl_->prefix_cache.size());
  return snapshot;
}

int KvBlockAllocator::SequenceBlockCount(const std::string& sequence_id) const {
  auto sequence_it = impl_->sequences.find(sequence_id);
  if (sequence_it == impl_->sequences.end()) {
    return 0;
  }
  return static_cast<int>(sequence_it->second.block_ids.size());
}

ContinuousBatcher::ContinuousBatcher(ContinuousBatcherConfig config)
    : config_(config) {
  config_.max_prefill_tokens_per_step =
      ClampPositive(config_.max_prefill_tokens_per_step, 64);
  config_.max_decode_sequences_per_step =
      ClampPositive(config_.max_decode_sequences_per_step, 8);
  config_.kv_cache_blocks = ClampPositive(config_.kv_cache_blocks, 1024);
  config_.kv_block_tokens = ClampPositive(config_.kv_block_tokens, 16);
}

ContinuousBatchingRun ContinuousBatcher::Run(
    const std::vector<SequenceRequest>& requests,
    const std::function<bool()>& is_cancelled) {
  struct State {
    SequenceRequest request;
    int index = 0;
    int prefill_remaining = 0;
    int generated = 0;
    bool prompt_allocated = false;
    bool prompt_ready = false;
    bool done = false;
  };

  ContinuousBatchingRun run;
  run.results.reserve(requests.size());
  std::vector<State> states;
  states.reserve(requests.size());

  for (int index = 0; index < static_cast<int>(requests.size()); ++index) {
    SequenceRequest request = requests[index];
    request.prompt_tokens = std::max(0, request.prompt_tokens);
    request.max_generated_tokens = std::max(0, request.max_generated_tokens);

    SequenceResult result;
    result.id = request.id;
    result.prompt_tokens = request.prompt_tokens;
    result.message = "queued";
    run.stats.total_prompt_tokens += request.prompt_tokens;
    run.results.push_back(std::move(result));

    State state;
    state.request = std::move(request);
    state.index = index;
    state.prefill_remaining = state.request.prompt_tokens;
    states.push_back(std::move(state));
  }

  KvCacheConfig kv_config;
  kv_config.total_blocks = config_.kv_cache_blocks;
  kv_config.tokens_per_block = config_.kv_block_tokens;
  KvBlockAllocator allocator(kv_config);

  auto unfinished_count = [&states] {
    return std::count_if(states.begin(), states.end(),
                         [](const State& state) { return !state.done; });
  };

  auto by_priority = [](const State* lhs, const State* rhs) {
    if (lhs->request.priority != rhs->request.priority) {
      return lhs->request.priority > rhs->request.priority;
    }
    return lhs->index < rhs->index;
  };

  for (int step_index = 0; unfinished_count() > 0; ++step_index) {
    if (is_cancelled()) {
      for (State& state : states) {
        if (state.done) {
          continue;
        }
        run.results[state.index].status = SequenceStatus::kCancelled;
        run.results[state.index].message = "cancelled";
        allocator.ReleaseSequence(state.request.id);
        state.done = true;
      }
      break;
    }

    SchedulerStep step;
    step.step_index = step_index;
    step.queued_sequences = unfinished_count();

    std::vector<State*> prefill_candidates;
    for (State& state : states) {
      if (!state.done && !state.prompt_ready) {
        prefill_candidates.push_back(&state);
      }
    }
    std::sort(prefill_candidates.begin(), prefill_candidates.end(),
              by_priority);

    int prefill_budget = config_.max_prefill_tokens_per_step;
    for (State* state : prefill_candidates) {
      if (prefill_budget <= 0) {
        break;
      }
      if (!state->prompt_allocated) {
        const auto allocation = allocator.AllocatePrompt(
            state->request.id, state->request.prompt_tokens,
            state->request.prefix_cache_key,
            state->request.prefix_cache_tokens);
        if (!allocation.ok) {
          SequenceResult& result = run.results[state->index];
          result.status = SequenceStatus::kRejected;
          result.message = allocation.message;
          result.kv_blocks = allocator.SequenceBlockCount(state->request.id);
          state->done = true;
          continue;
        }
        state->prompt_allocated = true;
        run.results[state->index].prefix_cache_hit = allocation.cache_hit;
      }

      const int consumed = std::min(prefill_budget, state->prefill_remaining);
      state->prefill_remaining -= consumed;
      step.prefill_tokens += consumed;
      prefill_budget -= consumed;
      if (state->prefill_remaining == 0) {
        state->prompt_ready = true;
      }
    }

    std::vector<State*> decode_candidates;
    for (State& state : states) {
      if (!state.done && state.prompt_ready &&
          state.generated < state.request.max_generated_tokens) {
        decode_candidates.push_back(&state);
      }
    }
    std::sort(decode_candidates.begin(), decode_candidates.end(),
              by_priority);

    const int decode_limit = std::min(
        config_.max_decode_sequences_per_step,
        static_cast<int>(decode_candidates.size()));
    for (int offset = 0; offset < decode_limit; ++offset) {
      State* state = decode_candidates[offset];
      const auto allocation = allocator.AppendTokens(state->request.id, 1);
      if (!allocation.ok) {
        SequenceResult& result = run.results[state->index];
        result.status = SequenceStatus::kRejected;
        result.message = allocation.message;
        result.generated_tokens = state->generated;
        result.kv_blocks = allocator.SequenceBlockCount(state->request.id);
        allocator.ReleaseSequence(state->request.id);
        state->done = true;
        continue;
      }

      ++state->generated;
      ++step.decoded_tokens;
      step.decoded_sequence_ids.push_back(state->request.id);
      if (run.results[state->index].first_token_step < 0) {
        run.results[state->index].first_token_step = step_index;
      }
      if (state->generated == state->request.max_generated_tokens) {
        SequenceResult& result = run.results[state->index];
        result.status = SequenceStatus::kCompleted;
        result.message = "completed";
        result.generated_tokens = state->generated;
        result.completion_step = step_index;
        result.kv_blocks = allocator.SequenceBlockCount(state->request.id);
        allocator.ReleaseSequence(state->request.id);
        state->done = true;
      }
    }

    for (State& state : states) {
      if (!state.done && state.prompt_ready &&
          state.request.max_generated_tokens == 0) {
        SequenceResult& result = run.results[state.index];
        result.status = SequenceStatus::kCompleted;
        result.message = "completed";
        result.completion_step = step_index;
        result.kv_blocks = allocator.SequenceBlockCount(state.request.id);
        allocator.ReleaseSequence(state.request.id);
        state.done = true;
      }
    }

    step.active_sequences = static_cast<int>(std::count_if(
        states.begin(), states.end(), [](const State& state) {
          return !state.done && state.prompt_ready;
        }));
    step.kv_blocks_used = allocator.Stats().used_blocks;
    if (step.prefill_tokens > 0) {
      ++run.stats.prefill_steps;
    }
    if (step.decoded_tokens > 0) {
      ++run.stats.decode_steps;
      run.stats.total_generated_tokens += step.decoded_tokens;
    }
    run.steps.push_back(std::move(step));
  }

  run.stats.total_steps = static_cast<int>(run.steps.size());
  if (run.stats.decode_steps > 0) {
    run.stats.average_decode_batch_size =
        static_cast<double>(run.stats.total_generated_tokens) /
        static_cast<double>(run.stats.decode_steps);
  }
  run.stats.kv_cache = allocator.Stats();
  return run;
}

std::string SequenceStatusName(SequenceStatus status) {
  switch (status) {
    case SequenceStatus::kCompleted:
      return "completed";
    case SequenceStatus::kRejected:
      return "rejected";
    case SequenceStatus::kCancelled:
      return "cancelled";
  }
  return "unknown";
}

}  // namespace laminar::backend::scheduling
