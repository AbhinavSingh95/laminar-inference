#include "backend/inference_backend.h"

#include <algorithm>
#include <array>
#include <cerrno>
#include <chrono>
#include <cctype>
#include <csignal>
#include <cstring>
#include <cmath>
#include <cstdlib>
#include <dlfcn.h>
#include <fcntl.h>
#include <filesystem>
#include <iomanip>
#include <limits>
#include <map>
#include <memory>
#include <netdb.h>
#include <numeric>
#include <optional>
#include <poll.h>
#include <sstream>
#include <string>
#include <sys/socket.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>
#include <utility>
#include <vector>

#include "backend/scheduling/continuous_batcher.h"
#include "onnxruntime_c_api.h"

using laminar::backend::scheduling::ContinuousBatcher;
using laminar::backend::scheduling::ContinuousBatcherConfig;
using laminar::backend::scheduling::ContinuousBatchingRun;
using laminar::backend::scheduling::KvCacheStats;
using laminar::backend::scheduling::SequenceRequest;
using laminar::backend::scheduling::SequenceResult;
using laminar::backend::scheduling::SequenceStatus;
using laminar::backend::scheduling::SequenceStatusName;

namespace {

constexpr int kInputDim = 128;
constexpr int kHiddenDim = 48;
constexpr int kClassCount = 4;
constexpr int kTinyLLMVocabSize = 64;
constexpr int kTinyLLMHiddenDim = 32;

const std::array<const char*, kClassCount> kLabels = {
    "latency_sensitive",
    "quality_seeking",
    "safety_review",
    "general",
};

const std::array<const char*, kTinyLLMVocabSize> kTinyLLMVocabulary = {
    "<bos>",       "<eos>",       "batching",    "latency",
    "throughput",  "queue",       "worker",      "trace",
    "token",       "prefill",     "decode",      "attention",
    "cache",       "runtime",     "model",       "adaptive",
    "system",      "request",     "response",    "metric",
    "collector",   "span",        "grpc",        "http",
    "onnx",        "scheduler",   "load",        "failure",
    "circuit",     "prompt",      "context",     "generation",
    "quality",     "safe",        "fast",        "deterministic",
    "serve",       "inference",   "pipeline",    "parallel",
    "microbatch",  "tail",        "p50",         "p95",
    "tokenizer",   "embedding",   "logit",       "sample",
    "topk",        "temperature", "hidden",      "layer",
    "position",    "causal",      "benchmark",   "evidence",
    "local",       "hermetic",    "control",     "data",
    "engine",      "memory",      "compute",     "finish",
};

std::string TrimLower(std::string value) {
  auto not_space = [](unsigned char ch) { return !std::isspace(ch); };
  value.erase(value.begin(), std::find_if(value.begin(), value.end(), not_space));
  value.erase(std::find_if(value.rbegin(), value.rend(), not_space).base(), value.end());
  std::transform(value.begin(), value.end(), value.begin(), [](unsigned char ch) {
    return static_cast<char>(std::tolower(ch));
  });
  return value;
}

std::string EnvString(const char* key, const std::string& fallback) {
  const char* raw = std::getenv(key);
  if (raw == nullptr || std::string(raw).empty()) {
    return fallback;
  }
  return raw;
}

int EnvInt(const char* key, int fallback, int minimum, int maximum) {
  const char* raw = std::getenv(key);
  if (raw == nullptr || std::string(raw).empty()) {
    return fallback;
  }
  char* end = nullptr;
  const long value = std::strtol(raw, &end, 10);
  if (end == raw || *end != '\0') {
    return fallback;
  }
  return static_cast<int>(std::clamp(value, static_cast<long>(minimum),
                                    static_cast<long>(maximum)));
}

float EnvFloat(const char* key, float fallback, float minimum, float maximum) {
  const char* raw = std::getenv(key);
  if (raw == nullptr || std::string(raw).empty()) {
    return fallback;
  }
  char* end = nullptr;
  const float value = std::strtof(raw, &end);
  if (end == raw || *end != '\0' || !std::isfinite(value)) {
    return fallback;
  }
  return std::clamp(value, minimum, maximum);
}

bool EnvBool(const char* key, bool fallback) {
  const char* raw = std::getenv(key);
  if (raw == nullptr || std::string(raw).empty()) {
    return fallback;
  }
  const std::string value = TrimLower(raw);
  if (value == "1" || value == "true" || value == "yes" || value == "on") {
    return true;
  }
  if (value == "0" || value == "false" || value == "no" || value == "off") {
    return false;
  }
  return fallback;
}

uint32_t HashToken(const std::string& token) {
  uint32_t hash = 2166136261u;
  for (unsigned char ch : token) {
    hash ^= ch;
    hash *= 16777619u;
  }
  return hash;
}

std::vector<std::string> Tokenize(const std::string& prompt) {
  std::vector<std::string> tokens;
  std::string token;
  for (unsigned char ch : prompt) {
    if (std::isalnum(ch)) {
      token.push_back(static_cast<char>(std::tolower(ch)));
      continue;
    }
    if (!token.empty()) {
      tokens.push_back(token);
      token.clear();
    }
  }
  if (!token.empty()) {
    tokens.push_back(token);
  }
  return tokens;
}

std::vector<int> TokenizeForTinyLLM(const std::string& prompt) {
  std::vector<int> token_ids;
  token_ids.push_back(0);
  for (const auto& token : Tokenize(prompt)) {
    token_ids.push_back(
        2 + static_cast<int>(HashToken(token) % (kTinyLLMVocabSize - 2)));
  }
  if (token_ids.size() == 1) {
    token_ids.push_back(2 + static_cast<int>(HashToken(prompt) %
                                            (kTinyLLMVocabSize - 2)));
  }
  return token_ids;
}

std::array<float, kInputDim> Featurize(const std::string& prompt) {
  std::array<float, kInputDim> features{};
  const auto tokens = Tokenize(prompt);
  const float token_scale = tokens.empty()
                                ? 1.0f
                                : 1.0f / std::sqrt(static_cast<float>(tokens.size()));

  int uppercase = 0;
  int digits = 0;
  int punctuation = 0;
  for (unsigned char ch : prompt) {
    uppercase += std::isupper(ch) ? 1 : 0;
    digits += std::isdigit(ch) ? 1 : 0;
    punctuation += std::ispunct(ch) ? 1 : 0;
  }

  const float length = static_cast<float>(std::max<size_t>(prompt.size(), 1));
  features[0] = 1.0f;
  features[1] = std::min(length / 256.0f, 1.0f);
  features[2] = std::min(static_cast<float>(tokens.size()) / 64.0f, 1.0f);
  features[3] = std::min(static_cast<float>(uppercase) / length, 1.0f);
  features[4] = std::min(static_cast<float>(digits) / length, 1.0f);
  features[5] = std::min(static_cast<float>(punctuation) / length, 1.0f);

  for (const auto& token : tokens) {
    const int index = 6 + static_cast<int>(HashToken(token) % (kInputDim - 6));
    features[index] += token_scale;
  }
  return features;
}

float InputWeight(int input, int hidden) {
  const int seed = (input + 17) * 37 + (hidden + 11) * 101;
  return 0.075f * std::sin(static_cast<float>(seed) * 0.017f);
}

float RecurrentWeight(int row, int col, int layer) {
  const int seed = (row + 3) * 53 + (col + 5) * 97 + (layer + 1) * 193;
  return 0.055f * std::cos(static_cast<float>(seed) * 0.013f);
}

float OutputWeight(int hidden, int klass) {
  const int seed = (hidden + 19) * 71 + (klass + 23) * 131;
  return 0.12f * std::sin(static_cast<float>(seed) * 0.011f);
}

float Bias(int index, float scale) {
  return scale * std::sin(static_cast<float>((index + 1) * 29) * 0.019f);
}

std::array<float, kClassCount> Softmax(std::array<float, kClassCount> logits) {
  const float max_logit = *std::max_element(logits.begin(), logits.end());
  float total = 0.0f;
  for (float& value : logits) {
    value = std::exp(value - max_logit);
    total += value;
  }
  for (float& value : logits) {
    value /= std::max(total, std::numeric_limits<float>::min());
  }
  return logits;
}

std::string FormatTinyModelOutput(const std::string& label, float confidence,
                                  int batch_size, int iterations) {
  std::ostringstream out;
  out << "TinyTextModel(backend=tiny_text_model, label=" << label
      << ", confidence=" << std::fixed << std::setprecision(3) << confidence
      << ", batch_size=" << batch_size
      << ", iterations=" << iterations << ")";
  return out.str();
}

float TinyLLMEmbedding(int token_id, int dim) {
  const int seed = (token_id + 7) * 131 + (dim + 3) * 17;
  return 0.5f * std::sin(static_cast<float>(seed) * 0.031f);
}

float TinyLLMProjection(int row, int col, int salt) {
  const int seed = (row + 5) * 73 + (col + 11) * 41 + salt * 149;
  return std::sin(static_cast<float>(seed) * 0.017f);
}

std::array<float, kTinyLLMHiddenDim> TinyLLMInputState(int token_id,
                                                       int position) {
  std::array<float, kTinyLLMHiddenDim> state{};
  for (int dim = 0; dim < kTinyLLMHiddenDim; ++dim) {
    const float position_signal =
        0.25f * std::sin(static_cast<float>((position + 1) * (dim + 1)) *
                         0.071f);
    state[dim] = TinyLLMEmbedding(token_id, dim) + position_signal;
  }
  return state;
}

float Dot(const std::array<float, kTinyLLMHiddenDim>& lhs,
          const std::array<float, kTinyLLMHiddenDim>& rhs, int salt) {
  float total = 0.0f;
  for (int dim = 0; dim < kTinyLLMHiddenDim; ++dim) {
    total += lhs[dim] * rhs[dim] *
             (0.75f + 0.25f * TinyLLMProjection(dim, salt, 1));
  }
  return total;
}

std::array<float, kTinyLLMHiddenDim> TinyLLMNextState(
    int token_id, int position,
    const std::vector<std::array<float, kTinyLLMHiddenDim>>& previous_states) {
  std::array<float, kTinyLLMHiddenDim> query =
      TinyLLMInputState(token_id, position);
  std::array<float, kTinyLLMHiddenDim> context{};

  if (!previous_states.empty()) {
    std::vector<float> scores;
    scores.reserve(previous_states.size());
    float max_score = -std::numeric_limits<float>::infinity();
    for (size_t i = 0; i < previous_states.size(); ++i) {
      const float distance_penalty =
          0.015f * static_cast<float>(previous_states.size() - i);
      const float score =
          Dot(query, previous_states[i], static_cast<int>(i)) /
              std::sqrt(static_cast<float>(kTinyLLMHiddenDim)) -
          distance_penalty;
      scores.push_back(score);
      max_score = std::max(max_score, score);
    }

    float total = 0.0f;
    for (float& score : scores) {
      score = std::exp(score - max_score);
      total += score;
    }
    total = std::max(total, std::numeric_limits<float>::min());

    for (size_t i = 0; i < previous_states.size(); ++i) {
      const float weight = scores[i] / total;
      for (int dim = 0; dim < kTinyLLMHiddenDim; ++dim) {
        context[dim] += weight * previous_states[i][dim];
      }
    }
  }

  std::array<float, kTinyLLMHiddenDim> output{};
  for (int dim = 0; dim < kTinyLLMHiddenDim; ++dim) {
    float ff = 0.0f;
    for (int hidden = 0; hidden < kTinyLLMHiddenDim; hidden += 4) {
      ff += query[hidden] * TinyLLMProjection(hidden, dim, 2);
      ff += context[hidden] * TinyLLMProjection(hidden, dim, 3);
    }
    output[dim] =
        std::tanh(0.58f * query[dim] + 0.37f * context[dim] + 0.05f * ff);
  }
  return output;
}

std::vector<std::array<float, kTinyLLMHiddenDim>> TinyLLMPrefill(
    const std::vector<int>& token_ids,
    const std::function<bool()>& is_cancelled) {
  std::vector<std::array<float, kTinyLLMHiddenDim>> states;
  states.reserve(token_ids.size());
  for (size_t position = 0; position < token_ids.size(); ++position) {
    if (is_cancelled()) {
      return {};
    }
    states.push_back(TinyLLMNextState(token_ids[position],
                                      static_cast<int>(position), states));
  }
  return states;
}

std::array<float, kTinyLLMVocabSize> TinyLLMLogits(
    const std::array<float, kTinyLLMHiddenDim>& state, float temperature) {
  std::array<float, kTinyLLMVocabSize> logits{};
  const float safe_temperature = std::max(temperature, 0.05f);
  for (int token = 0; token < kTinyLLMVocabSize; ++token) {
    float logit = 0.02f * std::sin(static_cast<float>(token + 1) * 0.37f);
    for (int dim = 0; dim < kTinyLLMHiddenDim; ++dim) {
      logit += state[dim] * TinyLLMProjection(dim, token, 5) * 0.15f;
    }
    if (token < 2) {
      logit -= 1.0f;
    }
    logits[token] = logit / safe_temperature;
  }
  return logits;
}

int TinyLLMSelectToken(const std::array<float, kTinyLLMVocabSize>& logits,
                       int top_k, uint32_t seed, int step) {
  std::vector<std::pair<float, int>> ranked;
  ranked.reserve(kTinyLLMVocabSize - 2);
  for (int token = 2; token < kTinyLLMVocabSize; ++token) {
    ranked.emplace_back(logits[token], token);
  }
  std::partial_sort(ranked.begin(),
                    ranked.begin() + std::min<int>(top_k, ranked.size()),
                    ranked.end(),
                    [](const auto& lhs, const auto& rhs) {
                      if (lhs.first == rhs.first) {
                        return lhs.second < rhs.second;
                      }
                      return lhs.first > rhs.first;
                    });
  const int capped_top_k =
      std::clamp(top_k, 1, static_cast<int>(ranked.size()));
  const int offset = static_cast<int>((seed + static_cast<uint32_t>(step * 17)) %
                                      static_cast<uint32_t>(capped_top_k));
  return ranked[offset].second;
}

std::string JoinGeneratedTokens(const std::vector<int>& token_ids) {
  std::ostringstream out;
  for (size_t i = 0; i < token_ids.size(); ++i) {
    if (i > 0) {
      out << ' ';
    }
    out << kTinyLLMVocabulary[token_ids[i]];
  }
  return out.str();
}

std::string FormatTinyLLMOutput(const std::string& generated,
                                int prompt_tokens, int generated_tokens,
                                long long prefill_micros,
                                long long decode_micros, int batch_size,
                                int max_generated_tokens, int top_k,
                                float temperature) {
  std::ostringstream out;
  out << "TinyLLM(backend=tiny_llm, generated='" << generated
      << "', prompt_tokens=" << prompt_tokens
      << ", generated_tokens=" << generated_tokens
      << ", prefill_micros=" << prefill_micros
      << ", decode_micros=" << decode_micros
      << ", batch_size=" << batch_size
      << ", max_generated_tokens=" << max_generated_tokens
      << ", top_k=" << top_k
      << ", temperature=" << std::fixed << std::setprecision(3)
      << temperature << ")";
  return out.str();
}

std::string FormatContinuousTinyLLMOutput(
    const std::string& generated, int prompt_tokens, int generated_tokens,
    long long prefill_micros, long long decode_micros, int batch_size,
    int max_generated_tokens, int top_k, float temperature,
    int first_token_step, int completion_step) {
  std::ostringstream out;
  out << "ContinuousTinyLLM(backend=continuous_tiny_llm, generated='"
      << generated
      << "', prompt_tokens=" << prompt_tokens
      << ", generated_tokens=" << generated_tokens
      << ", prefill_micros=" << prefill_micros
      << ", decode_micros=" << decode_micros
      << ", batch_size=" << batch_size
      << ", max_generated_tokens=" << max_generated_tokens
      << ", top_k=" << top_k
      << ", temperature=" << std::fixed << std::setprecision(3)
      << temperature
      << ", first_token_step=" << first_token_step
      << ", completion_step=" << completion_step
      << ")";
  return out.str();
}

struct TinyLLMGeneration {
  std::string generated;
  int prompt_tokens = 0;
  int generated_tokens = 0;
  long long prefill_micros = 0;
  long long decode_micros = 0;
};

TinyLLMGeneration GenerateTinyLLMCompletion(
    const std::string& prompt, int max_generated_tokens, float temperature,
    int top_k, const std::function<bool()>& is_cancelled) {
  TinyLLMGeneration generation;
  std::vector<int> token_ids = TokenizeForTinyLLM(prompt);
  generation.prompt_tokens = static_cast<int>(token_ids.size());

  const auto prefill_start = std::chrono::steady_clock::now();
  auto states = TinyLLMPrefill(token_ids, is_cancelled);
  const auto prefill_elapsed = std::chrono::steady_clock::now() - prefill_start;
  generation.prefill_micros =
      std::chrono::duration_cast<std::chrono::microseconds>(prefill_elapsed)
          .count();
  if (is_cancelled() || states.empty()) {
    return generation;
  }

  const auto decode_start = std::chrono::steady_clock::now();
  std::vector<int> generated_ids;
  generated_ids.reserve(max_generated_tokens);
  const uint32_t seed = HashToken(prompt);
  for (int step = 0; step < max_generated_tokens; ++step) {
    if (is_cancelled()) {
      return generation;
    }
    const auto logits = TinyLLMLogits(states.back(), temperature);
    const int next_token = TinyLLMSelectToken(logits, top_k, seed, step);
    generated_ids.push_back(next_token);
    token_ids.push_back(next_token);
    states.push_back(TinyLLMNextState(next_token,
                                      static_cast<int>(token_ids.size() - 1),
                                      states));
  }
  const auto decode_elapsed = std::chrono::steady_clock::now() - decode_start;
  generation.generated = JoinGeneratedTokens(generated_ids);
  generation.generated_tokens = static_cast<int>(generated_ids.size());
  generation.decode_micros =
      std::chrono::duration_cast<std::chrono::microseconds>(decode_elapsed)
          .count();
  return generation;
}

std::string FixedDouble(double value, int precision) {
  std::ostringstream out;
  out << std::fixed << std::setprecision(precision) << value;
  return out.str();
}

std::string PrefixCacheKey(const std::vector<int>& token_ids,
                           int prefix_cache_tokens) {
  if (prefix_cache_tokens <= 0 ||
      static_cast<int>(token_ids.size()) < prefix_cache_tokens) {
    return "";
  }
  std::ostringstream key;
  key << "tiny_llm";
  for (int index = 0; index < prefix_cache_tokens; ++index) {
    key << ':' << token_ids[index];
  }
  return key.str();
}

std::string DefaultOnnxRuntimeLibraryPath() {
#if defined(__APPLE__)
  return "third_party/onnxruntime/lib/libonnxruntime.dylib";
#elif defined(__linux__)
  return "third_party/onnxruntime/lib/libonnxruntime.so";
#else
  return "libonnxruntime";
#endif
}

float NormalizedHashFeature(const std::string& prompt) {
  const uint32_t hash = HashToken(prompt);
  return static_cast<float>(hash % 10000) / 10000.0f;
}

std::array<float, 2> IrisLikeFeatures(const std::string& prompt) {
  const float length_feature =
      std::min(static_cast<float>(prompt.size()) / 128.0f, 1.0f);
  return {length_feature, NormalizedHashFeature(prompt)};
}

BackendResult CancelledResult() {
  BackendResult result;
  result.code = BackendStatusCode::kCancelled;
  result.message = "batch cancelled";
  return result;
}

BackendResult UnavailableResult(const std::string& message) {
  BackendResult result;
  result.code = BackendStatusCode::kUnavailable;
  result.message = message;
  return result;
}

struct ProcessResult {
  int exit_code = -1;
  bool timed_out = false;
  bool cancelled = false;
  bool launch_failed = false;
  std::string output;
  std::string error;
};

struct ParsedHttpUrl {
  bool valid = false;
  std::string host;
  std::string port;
  std::string path;
};

struct HttpResponse {
  bool ok = false;
  int status_code = 0;
  long long first_response_byte_micros = -1;
  std::string body;
  std::string error;
};

struct LlamaServerCompletion {
  std::string content;
  int tokens_predicted = -1;
  int tokens_evaluated = -1;
  int stream_chunks = 0;
};

struct LlamaServerChunk {
  bool done = false;
  bool has_content = false;
  std::string content;
  int tokens_predicted = -1;
  int tokens_evaluated = -1;
};

bool ContainsPathSeparator(const std::string& path) {
  return path.find('/') != std::string::npos;
}

bool IsExecutableFile(const std::string& path) {
  std::error_code ignored;
  const auto status = std::filesystem::status(path, ignored);
  if (ignored || !std::filesystem::is_regular_file(status)) {
    return false;
  }
  return access(path.c_str(), X_OK) == 0;
}

std::string TrimWhitespace(std::string value) {
  auto not_space = [](unsigned char ch) { return !std::isspace(ch); };
  value.erase(value.begin(), std::find_if(value.begin(), value.end(), not_space));
  value.erase(std::find_if(value.rbegin(), value.rend(), not_space).base(), value.end());
  return value;
}

std::string CollapseWhitespace(const std::string& value) {
  std::ostringstream out;
  bool previous_space = false;
  for (unsigned char ch : value) {
    if (std::isspace(ch)) {
      if (!previous_space) {
        out << ' ';
      }
      previous_space = true;
      continue;
    }
    out << static_cast<char>(ch);
    previous_space = false;
  }
  return TrimWhitespace(out.str());
}

std::string EscapeSingleQuoted(const std::string& value) {
  std::ostringstream out;
  for (char ch : value) {
    if (ch == '\\' || ch == '\'') {
      out << '\\';
    }
    if (ch == '\n' || ch == '\r' || ch == '\t') {
      out << ' ';
      continue;
    }
    out << ch;
  }
  return out.str();
}

std::string Basename(const std::string& path) {
  if (path.empty()) {
    return "";
  }
  return std::filesystem::path(path).filename().string();
}

std::string FloatToString(float value) {
  std::ostringstream out;
  out << std::fixed << std::setprecision(3) << value;
  return out.str();
}

std::string BoolToJson(bool value) {
  return value ? "true" : "false";
}

std::string JsonEscape(const std::string& value) {
  std::ostringstream out;
  for (unsigned char ch : value) {
    switch (ch) {
      case '"':
        out << "\\\"";
        break;
      case '\\':
        out << "\\\\";
        break;
      case '\b':
        out << "\\b";
        break;
      case '\f':
        out << "\\f";
        break;
      case '\n':
        out << "\\n";
        break;
      case '\r':
        out << "\\r";
        break;
      case '\t':
        out << "\\t";
        break;
      default:
        if (ch < 0x20) {
          out << "\\u" << std::hex << std::setw(4) << std::setfill('0')
              << static_cast<int>(ch) << std::dec << std::setfill(' ');
        } else {
          out << static_cast<char>(ch);
        }
        break;
    }
  }
  return out.str();
}

std::string JsonStringValue(const std::string& value) {
  return "\"" + JsonEscape(value) + "\"";
}

std::string BuildLlamaServerRequestBody(const std::string& prompt,
                                        int max_tokens, float temperature,
                                        bool cache_prompt, bool stream,
                                        bool timings_per_token) {
  std::ostringstream body;
  body << "{"
       << "\"prompt\":" << JsonStringValue(prompt)
       << ",\"n_predict\":" << max_tokens
       << ",\"temperature\":" << FloatToString(temperature)
       << ",\"stream\":" << BoolToJson(stream)
       << ",\"cache_prompt\":" << BoolToJson(cache_prompt)
       << ",\"return_tokens\":" << BoolToJson(timings_per_token)
       << ",\"timings_per_token\":" << BoolToJson(timings_per_token)
       << "}";
  return body.str();
}

ParsedHttpUrl ParseHttpUrl(const std::string& raw_url) {
  constexpr const char* kHttpPrefix = "http://";
  ParsedHttpUrl parsed;
  if (raw_url.rfind(kHttpPrefix, 0) != 0) {
    return parsed;
  }
  std::string rest = raw_url.substr(std::strlen(kHttpPrefix));
  const size_t slash = rest.find('/');
  std::string authority = slash == std::string::npos ? rest : rest.substr(0, slash);
  parsed.path = slash == std::string::npos ? "/" : rest.substr(slash);
  if (authority.empty()) {
    return parsed;
  }
  const size_t colon = authority.rfind(':');
  if (colon == std::string::npos) {
    parsed.host = authority;
    parsed.port = "80";
  } else {
    parsed.host = authority.substr(0, colon);
    parsed.port = authority.substr(colon + 1);
  }
  parsed.valid = !parsed.host.empty() && !parsed.port.empty() &&
                 !parsed.path.empty();
  return parsed;
}

std::string EndpointForOutput(const std::string& raw_url) {
  ParsedHttpUrl parsed = ParseHttpUrl(raw_url);
  if (!parsed.valid) {
    return raw_url;
  }
  return parsed.host + ":" + parsed.port + parsed.path;
}

bool SetSocketTimeout(int fd, int timeout_ms) {
  timeval timeout{};
  timeout.tv_sec = timeout_ms / 1000;
  timeout.tv_usec = (timeout_ms % 1000) * 1000;
  return setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof(timeout)) == 0 &&
         setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, sizeof(timeout)) == 0;
}

bool ConnectWithTimeout(int fd, const sockaddr* addr, socklen_t addrlen,
                        int timeout_ms) {
  const int flags = fcntl(fd, F_GETFL, 0);
  if (flags < 0) {
    return false;
  }
  if (fcntl(fd, F_SETFL, flags | O_NONBLOCK) < 0) {
    return false;
  }
  const int rc = connect(fd, addr, addrlen);
  if (rc == 0) {
    fcntl(fd, F_SETFL, flags);
    return true;
  }
  if (errno != EINPROGRESS) {
    return false;
  }
  pollfd descriptor{};
  descriptor.fd = fd;
  descriptor.events = POLLOUT;
  const int poll_result = poll(&descriptor, 1, timeout_ms);
  if (poll_result <= 0) {
    return false;
  }
  int socket_error = 0;
  socklen_t socket_error_len = sizeof(socket_error);
  if (getsockopt(fd, SOL_SOCKET, SO_ERROR, &socket_error,
                 &socket_error_len) != 0 ||
      socket_error != 0) {
    return false;
  }
  fcntl(fd, F_SETFL, flags);
  return true;
}

HttpResponse HttpPostJson(const ParsedHttpUrl& url, const std::string& body,
                          int timeout_ms,
                          const std::function<bool()>& is_cancelled) {
  HttpResponse response;
  const auto started = std::chrono::steady_clock::now();
  addrinfo hints{};
  hints.ai_family = AF_UNSPEC;
  hints.ai_socktype = SOCK_STREAM;
  addrinfo* addresses = nullptr;
  const int gai = getaddrinfo(url.host.c_str(), url.port.c_str(), &hints,
                              &addresses);
  if (gai != 0) {
    response.error = "getaddrinfo failed for " + url.host + ":" + url.port;
    return response;
  }

  int fd = -1;
  for (addrinfo* address = addresses; address != nullptr;
       address = address->ai_next) {
    if (is_cancelled()) {
      freeaddrinfo(addresses);
      response.error = "cancelled";
      return response;
    }
    fd = socket(address->ai_family, address->ai_socktype, address->ai_protocol);
    if (fd < 0) {
      continue;
    }
    if (ConnectWithTimeout(fd, address->ai_addr,
                           static_cast<socklen_t>(address->ai_addrlen),
                           timeout_ms)) {
      break;
    }
    close(fd);
    fd = -1;
  }
  freeaddrinfo(addresses);

  if (fd < 0) {
    response.error = "failed to connect to llama server at " + url.host + ":" +
                     url.port;
    return response;
  }
  SetSocketTimeout(fd, timeout_ms);

  std::ostringstream request;
  request << "POST " << url.path << " HTTP/1.1\r\n"
          << "Host: " << url.host << ":" << url.port << "\r\n"
          << "Content-Type: application/json\r\n"
          << "Content-Length: " << body.size() << "\r\n"
          << "Connection: close\r\n\r\n"
          << body;
  const std::string request_text = request.str();
  size_t written = 0;
  while (written < request_text.size()) {
    if (is_cancelled()) {
      close(fd);
      response.error = "cancelled";
      return response;
    }
    const ssize_t bytes = send(fd, request_text.data() + written,
                               request_text.size() - written, 0);
    if (bytes <= 0) {
      response.error = "failed to send request to llama server";
      close(fd);
      return response;
    }
    written += static_cast<size_t>(bytes);
  }

  std::string raw_response;
  char buffer[4096];
  while (true) {
    if (is_cancelled()) {
      close(fd);
      response.error = "cancelled";
      return response;
    }
    const ssize_t bytes = recv(fd, buffer, sizeof(buffer), 0);
    if (bytes > 0) {
      if (response.first_response_byte_micros < 0) {
        const auto elapsed = std::chrono::steady_clock::now() - started;
        response.first_response_byte_micros =
            std::chrono::duration_cast<std::chrono::microseconds>(elapsed)
                .count();
      }
      raw_response.append(buffer, static_cast<size_t>(bytes));
      if (raw_response.size() > 1024 * 1024) {
        response.error = "llama server response exceeded 1 MiB";
        close(fd);
        return response;
      }
      continue;
    }
    if (bytes == 0) {
      break;
    }
    if (errno == EINTR) {
      continue;
    }
    response.error = "failed to read response from llama server";
    close(fd);
    return response;
  }
  close(fd);

  const size_t header_end = raw_response.find("\r\n\r\n");
  if (header_end == std::string::npos) {
    response.error = "llama server response was missing HTTP headers";
    return response;
  }
  const std::string status_line =
      raw_response.substr(0, raw_response.find("\r\n"));
  std::istringstream status_stream(status_line);
  std::string http_version;
  status_stream >> http_version >> response.status_code;
  response.body = raw_response.substr(header_end + 4);
  response.ok = response.status_code >= 200 && response.status_code < 300;
  if (!response.ok) {
    response.error = "llama server returned HTTP " +
                     std::to_string(response.status_code);
  }
  return response;
}

HttpResponse HttpPostJsonStream(
    const ParsedHttpUrl& url, const std::string& body, int timeout_ms,
    const std::function<bool()>& is_cancelled,
    const std::function<bool(const char* data, size_t size)>& on_body) {
  HttpResponse response;
  const auto started = std::chrono::steady_clock::now();
  addrinfo hints{};
  hints.ai_family = AF_UNSPEC;
  hints.ai_socktype = SOCK_STREAM;
  addrinfo* addresses = nullptr;
  const int gai = getaddrinfo(url.host.c_str(), url.port.c_str(), &hints,
                              &addresses);
  if (gai != 0) {
    response.error = "getaddrinfo failed for " + url.host + ":" + url.port;
    return response;
  }

  int fd = -1;
  for (addrinfo* address = addresses; address != nullptr;
       address = address->ai_next) {
    if (is_cancelled()) {
      freeaddrinfo(addresses);
      response.error = "cancelled";
      return response;
    }
    fd = socket(address->ai_family, address->ai_socktype, address->ai_protocol);
    if (fd < 0) {
      continue;
    }
    if (ConnectWithTimeout(fd, address->ai_addr,
                           static_cast<socklen_t>(address->ai_addrlen),
                           timeout_ms)) {
      break;
    }
    close(fd);
    fd = -1;
  }
  freeaddrinfo(addresses);

  if (fd < 0) {
    response.error = "failed to connect to llama server at " + url.host + ":" +
                     url.port;
    return response;
  }
  SetSocketTimeout(fd, timeout_ms);

  std::ostringstream request;
  request << "POST " << url.path << " HTTP/1.1\r\n"
          << "Host: " << url.host << ":" << url.port << "\r\n"
          << "Content-Type: application/json\r\n"
          << "Content-Length: " << body.size() << "\r\n"
          << "Connection: close\r\n\r\n"
          << body;
  const std::string request_text = request.str();
  size_t written = 0;
  while (written < request_text.size()) {
    if (is_cancelled()) {
      close(fd);
      response.error = "cancelled";
      return response;
    }
    const ssize_t bytes = send(fd, request_text.data() + written,
                               request_text.size() - written, 0);
    if (bytes <= 0) {
      response.error = "failed to send request to llama server";
      close(fd);
      return response;
    }
    written += static_cast<size_t>(bytes);
  }

  bool headers_parsed = false;
  std::string buffered;
  char buffer[4096];
  while (true) {
    if (is_cancelled()) {
      close(fd);
      response.error = "cancelled";
      return response;
    }
    const ssize_t bytes = recv(fd, buffer, sizeof(buffer), 0);
    if (bytes > 0) {
      if (response.first_response_byte_micros < 0) {
        const auto elapsed = std::chrono::steady_clock::now() - started;
        response.first_response_byte_micros =
            std::chrono::duration_cast<std::chrono::microseconds>(elapsed)
                .count();
      }

      if (!headers_parsed) {
        buffered.append(buffer, static_cast<size_t>(bytes));
        const size_t header_end = buffered.find("\r\n\r\n");
        if (header_end == std::string::npos) {
          if (buffered.size() > 64 * 1024) {
            response.error = "llama server response headers exceeded 64 KiB";
            close(fd);
            return response;
          }
          continue;
        }

        const std::string status_line =
            buffered.substr(0, buffered.find("\r\n"));
        std::istringstream status_stream(status_line);
        std::string http_version;
        status_stream >> http_version >> response.status_code;
        response.ok =
            response.status_code >= 200 && response.status_code < 300;
        headers_parsed = true;

        const std::string body_piece = buffered.substr(header_end + 4);
        buffered.clear();
        if (!body_piece.empty()) {
          if (response.ok) {
            if (!on_body(body_piece.data(), body_piece.size())) {
              response.error = "cancelled";
              close(fd);
              return response;
            }
          } else {
            response.body += body_piece;
          }
        }
        continue;
      }

      if (response.ok) {
        if (!on_body(buffer, static_cast<size_t>(bytes))) {
          response.error = "cancelled";
          close(fd);
          return response;
        }
      } else {
        response.body.append(buffer, static_cast<size_t>(bytes));
        if (response.body.size() > 1024 * 1024) {
          response.error = "llama server response exceeded 1 MiB";
          close(fd);
          return response;
        }
      }
      continue;
    }
    if (bytes == 0) {
      break;
    }
    if (errno == EINTR) {
      continue;
    }
    response.error = "failed to read response from llama server";
    close(fd);
    return response;
  }
  close(fd);

  if (!headers_parsed) {
    response.error = "llama server response was missing HTTP headers";
    return response;
  }
  if (!response.ok) {
    response.error = "llama server returned HTTP " +
                     std::to_string(response.status_code);
  }
  return response;
}

std::optional<std::string> ExtractJsonStringField(const std::string& json,
                                                  const std::string& field) {
  const std::string key = "\"" + field + "\"";
  size_t pos = json.find(key);
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  pos = json.find(':', pos + key.size());
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  ++pos;
  while (pos < json.size() &&
         std::isspace(static_cast<unsigned char>(json[pos]))) {
    ++pos;
  }
  if (pos >= json.size() || json[pos] != '"') {
    return std::nullopt;
  }
  ++pos;
  std::string value;
  bool escaping = false;
  for (; pos < json.size(); ++pos) {
    const char ch = json[pos];
    if (escaping) {
      switch (ch) {
        case 'n':
          value.push_back('\n');
          break;
        case 'r':
          value.push_back('\r');
          break;
        case 't':
          value.push_back('\t');
          break;
        case '"':
        case '\\':
        case '/':
          value.push_back(ch);
          break;
        default:
          value.push_back(ch);
          break;
      }
      escaping = false;
      continue;
    }
    if (ch == '\\') {
      escaping = true;
      continue;
    }
    if (ch == '"') {
      return value;
    }
    value.push_back(ch);
  }
  return std::nullopt;
}

std::optional<int> ExtractJsonIntField(const std::string& json,
                                       const std::string& field) {
  const std::string key = "\"" + field + "\"";
  size_t pos = json.find(key);
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  pos = json.find(':', pos + key.size());
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  ++pos;
  while (pos < json.size() &&
         std::isspace(static_cast<unsigned char>(json[pos]))) {
    ++pos;
  }
  char* end = nullptr;
  const long value = std::strtol(json.c_str() + pos, &end, 10);
  if (end == json.c_str() + pos) {
    return std::nullopt;
  }
  return static_cast<int>(value);
}

LlamaServerChunk ParseLlamaServerChunk(const std::string& data) {
  LlamaServerChunk chunk;
  const std::string trimmed = TrimWhitespace(data);
  if (trimmed == "[DONE]") {
    chunk.done = true;
    return chunk;
  }
  const auto content = ExtractJsonStringField(trimmed, "content");
  if (content.has_value()) {
    chunk.has_content = true;
    chunk.content = *content;
  }
  const auto tokens_predicted =
      ExtractJsonIntField(trimmed, "tokens_predicted");
  if (tokens_predicted.has_value()) {
    chunk.tokens_predicted = *tokens_predicted;
  }
  const auto tokens_evaluated =
      ExtractJsonIntField(trimmed, "tokens_evaluated");
  if (tokens_evaluated.has_value()) {
    chunk.tokens_evaluated = *tokens_evaluated;
  }
  return chunk;
}

void MergeLlamaServerJsonChunk(const std::string& json,
                               LlamaServerCompletion* completion) {
  const LlamaServerChunk chunk = ParseLlamaServerChunk(json);
  if (chunk.done) {
    return;
  }
  if (chunk.has_content) {
    completion->content += chunk.content;
  }
  if (chunk.tokens_predicted >= 0) {
    completion->tokens_predicted = chunk.tokens_predicted;
  }
  if (chunk.tokens_evaluated >= 0) {
    completion->tokens_evaluated = chunk.tokens_evaluated;
  }
}

LlamaServerCompletion ParseLlamaServerCompletion(const std::string& body,
                                                 bool stream) {
  LlamaServerCompletion completion;
  if (!stream) {
    MergeLlamaServerJsonChunk(body, &completion);
    return completion;
  }

  std::istringstream lines(body);
  std::string line;
  while (std::getline(lines, line)) {
    line = TrimWhitespace(line);
    if (line.rfind("data:", 0) != 0) {
      continue;
    }
    std::string data = TrimWhitespace(line.substr(std::strlen("data:")));
    if (data.empty() || data == "[DONE]") {
      continue;
    }
    ++completion.stream_chunks;
    MergeLlamaServerJsonChunk(data, &completion);
  }
  return completion;
}

std::map<std::string, std::string> BuildLlamaServerMetadata(
    bool stream, bool cache_prompt, bool timings_per_token,
    const LlamaServerCompletion& completion, size_t prompt_chars,
    size_t generated_chars, long long ttft_micros) {
  return {
      {"stream", BoolToJson(stream)},
      {"cache_prompt", BoolToJson(cache_prompt)},
      {"timings_per_token", BoolToJson(timings_per_token)},
      {"stream_chunks", std::to_string(completion.stream_chunks)},
      {"ttft_micros", std::to_string(ttft_micros)},
      {"tokens_predicted", std::to_string(completion.tokens_predicted)},
      {"tokens_evaluated", std::to_string(completion.tokens_evaluated)},
      {"prompt_chars", std::to_string(prompt_chars)},
      {"generated_chars", std::to_string(generated_chars)},
  };
}

std::string FormatLlamaServerOutput(const std::string& generated,
                                    const std::string& endpoint,
                                    int prompt_chars, int generated_chars,
                                    long long duration_micros, int batch_size,
                                    int max_tokens, float temperature,
                                    bool cache_prompt, bool stream,
                                    bool timings_per_token, int stream_chunks,
                                    long long ttft_micros,
                                    int tokens_predicted, int tokens_evaluated) {
  std::ostringstream out;
  out << "LlamaServer(backend=llama_server, generated='"
      << EscapeSingleQuoted(generated)
      << "', prompt_chars=" << prompt_chars
      << ", generated_chars=" << generated_chars
      << ", duration_micros=" << duration_micros
      << ", batch_size=" << batch_size
      << ", endpoint='" << EscapeSingleQuoted(endpoint) << "'"
      << ", max_tokens=" << max_tokens
      << ", temperature=" << std::fixed << std::setprecision(3)
      << temperature
      << ", cache_prompt=" << BoolToJson(cache_prompt)
      << ", stream=" << BoolToJson(stream)
      << ", timings_per_token=" << BoolToJson(timings_per_token)
      << ", stream_chunks=" << stream_chunks
      << ", ttft_micros=" << ttft_micros
      << ", tokens_predicted=" << tokens_predicted
      << ", tokens_evaluated=" << tokens_evaluated << ")";
  return out.str();
}

void AppendWithLimit(std::string* output, const char* data, size_t size,
                     size_t limit) {
  if (output->size() >= limit) {
    return;
  }
  const size_t available = limit - output->size();
  output->append(data, std::min(size, available));
}

void ReadAvailable(int fd, std::string* output, size_t limit) {
  char buffer[4096];
  while (true) {
    const ssize_t bytes = read(fd, buffer, sizeof(buffer));
    if (bytes > 0) {
      AppendWithLimit(output, buffer, static_cast<size_t>(bytes), limit);
      continue;
    }
    if (bytes == 0) {
      return;
    }
    if (errno == EINTR) {
      continue;
    }
    if (errno == EAGAIN || errno == EWOULDBLOCK) {
      return;
    }
    return;
  }
}

void SignalProcessGroup(pid_t pid, int signal_number) {
  kill(-pid, signal_number);
  kill(pid, signal_number);
}

void StopProcess(pid_t pid, int* status) {
  SignalProcessGroup(pid, SIGTERM);
  for (int attempt = 0; attempt < 30; ++attempt) {
    const pid_t done = waitpid(pid, status, WNOHANG);
    if (done == pid) {
      return;
    }
    if (done < 0 && errno != EINTR) {
      return;
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(10));
  }
  SignalProcessGroup(pid, SIGKILL);
  while (waitpid(pid, status, 0) < 0 && errno == EINTR) {
  }
}

ProcessResult RunProcess(const std::vector<std::string>& args,
                         std::chrono::milliseconds timeout,
                         const std::function<bool()>& is_cancelled) {
  ProcessResult result;
  if (args.empty()) {
    result.launch_failed = true;
    result.error = "empty command";
    return result;
  }

  int pipe_fds[2];
  if (pipe(pipe_fds) != 0) {
    result.launch_failed = true;
    result.error = std::string("pipe failed: ") + std::strerror(errno);
    return result;
  }

  const pid_t pid = fork();
  if (pid < 0) {
    const int saved_errno = errno;
    close(pipe_fds[0]);
    close(pipe_fds[1]);
    result.launch_failed = true;
    result.error = std::string("fork failed: ") + std::strerror(saved_errno);
    return result;
  }

  if (pid == 0) {
    setpgid(0, 0);
    close(pipe_fds[0]);
    dup2(pipe_fds[1], STDOUT_FILENO);
    dup2(pipe_fds[1], STDERR_FILENO);
    close(pipe_fds[1]);

    std::vector<char*> argv;
    argv.reserve(args.size() + 1);
    for (const auto& arg : args) {
      argv.push_back(const_cast<char*>(arg.c_str()));
    }
    argv.push_back(nullptr);

    execvp(argv[0], argv.data());
    const std::string message =
        std::string("exec failed for ") + args[0] + ": " +
        std::strerror(errno) + "\n";
    write(STDERR_FILENO, message.data(), message.size());
    _exit(127);
  }

  close(pipe_fds[1]);
  const int flags = fcntl(pipe_fds[0], F_GETFL, 0);
  if (flags >= 0) {
    fcntl(pipe_fds[0], F_SETFL, flags | O_NONBLOCK);
  }

  constexpr size_t kMaxCapturedOutputBytes = 16 * 1024;
  const auto deadline = std::chrono::steady_clock::now() + timeout;
  int status = 0;
  bool exited = false;

  while (true) {
    ReadAvailable(pipe_fds[0], &result.output, kMaxCapturedOutputBytes);

    const pid_t done = waitpid(pid, &status, WNOHANG);
    if (done == pid) {
      exited = true;
      break;
    }
    if (done < 0 && errno != EINTR) {
      result.launch_failed = true;
      result.error = std::string("waitpid failed: ") + std::strerror(errno);
      break;
    }

    if (is_cancelled()) {
      result.cancelled = true;
      break;
    }

    const auto now = std::chrono::steady_clock::now();
    if (now >= deadline) {
      result.timed_out = true;
      break;
    }

    const auto remaining =
        std::chrono::duration_cast<std::chrono::milliseconds>(deadline - now);
    const int poll_timeout_ms =
        static_cast<int>(std::clamp<long long>(remaining.count(), 1, 25));
    pollfd fd;
    fd.fd = pipe_fds[0];
    fd.events = POLLIN | POLLHUP;
    fd.revents = 0;
    poll(&fd, 1, poll_timeout_ms);
  }

  if (!exited) {
    StopProcess(pid, &status);
  }

  ReadAvailable(pipe_fds[0], &result.output, kMaxCapturedOutputBytes);
  close(pipe_fds[0]);

  if (WIFEXITED(status)) {
    result.exit_code = WEXITSTATUS(status);
  } else if (WIFSIGNALED(status)) {
    result.exit_code = 128 + WTERMSIG(status);
  }
  result.output = TrimWhitespace(result.output);
  return result;
}

std::vector<std::string> BuildLlamaCppCommand(
    const std::string& binary_path, const std::string& model_path,
    const std::string& prompt, int max_tokens, int context_size, int threads,
    float temperature) {
  std::vector<std::string> args = {
      binary_path,
      "-m",
      model_path,
      "-p",
      prompt,
      "-n",
      std::to_string(max_tokens),
      "-c",
      std::to_string(context_size),
      "--temp",
      FloatToString(temperature),
      "--no-display-prompt",
      "--no-show-timings",
  };
  if (threads > 0) {
    args.push_back("-t");
    args.push_back(std::to_string(threads));
  }
  return args;
}

std::string FormatLlamaCppOutput(const std::string& generated,
                                 const std::string& model_path,
                                 int prompt_chars, int generated_chars,
                                 long long duration_micros, int batch_size,
                                 int max_tokens, int context_size, int threads,
                                 float temperature) {
  std::ostringstream out;
  out << "LlamaCpp(backend=llama_cpp, generated='"
      << EscapeSingleQuoted(generated)
      << "', prompt_chars=" << prompt_chars
      << ", generated_chars=" << generated_chars
      << ", duration_micros=" << duration_micros
      << ", batch_size=" << batch_size
      << ", model='" << EscapeSingleQuoted(Basename(model_path)) << "'"
      << ", max_tokens=" << max_tokens
      << ", ctx_size=" << context_size
      << ", threads=" << threads
      << ", temperature=" << std::fixed << std::setprecision(3)
      << temperature << ")";
  return out.str();
}

}  // namespace

BackendResult InferenceBackend::ProcessStream(
    const BackendRequest& request, const std::function<bool()>& is_cancelled,
    const BackendStreamCallback& emit) {
  BackendResult result = ProcessBatch(std::vector<BackendRequest>{request},
                                      is_cancelled);
  if (result.code != BackendStatusCode::kOk) {
    return result;
  }
  if (result.outputs.size() != 1) {
    return UnavailableResult("backend returned the wrong number of outputs");
  }
  BackendStreamEvent event;
  event.event_type = "done";
  event.result = result.outputs[0];
  if (!result.output_metadata.empty()) {
    event.metadata = result.output_metadata[0];
  }
  if (!emit(event)) {
    return CancelledResult();
  }
  return result;
}

SimulatorBackend::SimulatorBackend(int base_latency_ms, int per_request_latency_ms)
    : base_latency_ms_(std::max(base_latency_ms, 0)),
      per_request_latency_ms_(std::max(per_request_latency_ms, 0)) {}

std::string SimulatorBackend::Name() const { return "simulator"; }

BackendResult SimulatorBackend::ProcessBatch(
    const std::vector<BackendRequest>& requests,
    const std::function<bool()>& is_cancelled) {
  const int batch_size = static_cast<int>(requests.size());
  const int total_latency_ms =
      base_latency_ms_ + (batch_size * per_request_latency_ms_);

  int remaining_latency_ms = total_latency_ms;
  while (remaining_latency_ms > 0) {
    if (is_cancelled()) {
      return CancelledResult();
    }
    const int step_ms = std::min(remaining_latency_ms, 5);
    std::this_thread::sleep_for(std::chrono::milliseconds(step_ms));
    remaining_latency_ms -= step_ms;
  }

  if (is_cancelled()) {
    return CancelledResult();
  }

  BackendResult result;
  result.outputs.reserve(requests.size());
  for (const auto& request : requests) {
    result.outputs.push_back(
        "Processed: '" + request.prompt + "' (backend=simulator, batch_size=" +
        std::to_string(batch_size) + ", latency=" +
        std::to_string(total_latency_ms) + "ms)");
  }
  return result;
}

TinyTextModelBackend::TinyTextModelBackend(int iterations)
    : iterations_(std::clamp(iterations, 1, 1024)) {}

std::string TinyTextModelBackend::Name() const { return "tiny_text_model"; }

BackendResult TinyTextModelBackend::ProcessBatch(
    const std::vector<BackendRequest>& requests,
    const std::function<bool()>& is_cancelled) {
  if (is_cancelled()) {
    return CancelledResult();
  }

  BackendResult result;
  result.outputs.reserve(requests.size());

  for (const auto& request : requests) {
    if (is_cancelled()) {
      return CancelledResult();
    }

    const auto features = Featurize(request.prompt);
    std::array<float, kHiddenDim> hidden{};
    for (int j = 0; j < kHiddenDim; ++j) {
      float sum = Bias(j, 0.05f);
      for (int i = 0; i < kInputDim; ++i) {
        sum += features[i] * InputWeight(i, j);
      }
      hidden[j] = std::tanh(sum);
    }

    for (int layer = 0; layer < iterations_; ++layer) {
      if (is_cancelled()) {
        return CancelledResult();
      }
      std::array<float, kHiddenDim> next{};
      for (int j = 0; j < kHiddenDim; ++j) {
        float sum = Bias(j + layer, 0.03f);
        for (int k = 0; k < kHiddenDim; ++k) {
          sum += hidden[k] * RecurrentWeight(k, j, layer);
        }
        next[j] = std::tanh(sum);
      }
      hidden = next;
    }

    std::array<float, kClassCount> logits{};
    for (int c = 0; c < kClassCount; ++c) {
      float sum = Bias(c, 0.02f);
      for (int j = 0; j < kHiddenDim; ++j) {
        sum += hidden[j] * OutputWeight(j, c);
      }
      logits[c] = sum;
    }

    const auto probabilities = Softmax(logits);
    const auto best =
        std::max_element(probabilities.begin(), probabilities.end());
    const int label_index =
        static_cast<int>(std::distance(probabilities.begin(), best));
    result.outputs.push_back(FormatTinyModelOutput(
        kLabels[label_index], *best, static_cast<int>(requests.size()),
        iterations_));
  }

  return result;
}

TinyLLMBackend::TinyLLMBackend(int max_generated_tokens, float temperature,
                               int top_k)
    : max_generated_tokens_(std::clamp(max_generated_tokens, 1, 64)),
      temperature_(std::clamp(temperature, 0.05f, 2.0f)),
      top_k_(std::clamp(top_k, 1, kTinyLLMVocabSize - 2)) {}

std::string TinyLLMBackend::Name() const { return "tiny_llm"; }

BackendResult TinyLLMBackend::ProcessBatch(
    const std::vector<BackendRequest>& requests,
    const std::function<bool()>& is_cancelled) {
  if (is_cancelled()) {
    return CancelledResult();
  }

  BackendResult result;
  result.outputs.reserve(requests.size());

  for (const auto& request : requests) {
    if (is_cancelled()) {
      return CancelledResult();
    }

    std::vector<int> token_ids = TokenizeForTinyLLM(request.prompt);

    const auto prefill_start = std::chrono::steady_clock::now();
    auto states = TinyLLMPrefill(token_ids, is_cancelled);
    const auto prefill_elapsed = std::chrono::steady_clock::now() - prefill_start;
    if (is_cancelled()) {
      return CancelledResult();
    }
    if (states.empty()) {
      return CancelledResult();
    }

    const auto decode_start = std::chrono::steady_clock::now();
    std::vector<int> generated_ids;
    generated_ids.reserve(max_generated_tokens_);
    const uint32_t seed = HashToken(request.prompt);
    for (int step = 0; step < max_generated_tokens_; ++step) {
      if (is_cancelled()) {
        return CancelledResult();
      }
      const auto logits = TinyLLMLogits(states.back(), temperature_);
      const int next_token = TinyLLMSelectToken(logits, top_k_, seed, step);
      generated_ids.push_back(next_token);
      token_ids.push_back(next_token);
      states.push_back(TinyLLMNextState(next_token,
                                        static_cast<int>(token_ids.size() - 1),
                                        states));
    }
    const auto decode_elapsed = std::chrono::steady_clock::now() - decode_start;

    result.outputs.push_back(FormatTinyLLMOutput(
        JoinGeneratedTokens(generated_ids),
        static_cast<int>(token_ids.size() - generated_ids.size()),
        static_cast<int>(generated_ids.size()),
        std::chrono::duration_cast<std::chrono::microseconds>(prefill_elapsed)
            .count(),
        std::chrono::duration_cast<std::chrono::microseconds>(decode_elapsed)
            .count(),
        static_cast<int>(requests.size()), max_generated_tokens_, top_k_,
        temperature_));
  }

  return result;
}

ContinuousTinyLLMBackend::ContinuousTinyLLMBackend(
    int max_generated_tokens, float temperature, int top_k,
    int max_prefill_tokens_per_step, int max_decode_sequences_per_step,
    int kv_cache_blocks, int kv_block_tokens, int prefix_cache_tokens)
    : max_generated_tokens_(std::clamp(max_generated_tokens, 1, 64)),
      temperature_(std::clamp(temperature, 0.05f, 2.0f)),
      top_k_(std::clamp(top_k, 1, kTinyLLMVocabSize - 2)),
      max_prefill_tokens_per_step_(
          std::clamp(max_prefill_tokens_per_step, 1, 1 << 20)),
      max_decode_sequences_per_step_(
          std::clamp(max_decode_sequences_per_step, 1, 1 << 20)),
      kv_cache_blocks_(std::clamp(kv_cache_blocks, 1, 1 << 20)),
      kv_block_tokens_(std::clamp(kv_block_tokens, 1, 1 << 20)),
      prefix_cache_tokens_(std::clamp(prefix_cache_tokens, 0, 1024)) {}

std::string ContinuousTinyLLMBackend::Name() const {
  return "continuous_tiny_llm";
}

std::map<std::string, std::string> BuildContinuousTinyLLMMetadata(
    const ContinuousBatchingRun& run, const SequenceResult& sequence) {
  const KvCacheStats& kv = run.stats.kv_cache;
  return {
      {"sequence.status", SequenceStatusName(sequence.status)},
      {"sequence.generated_tokens", std::to_string(sequence.generated_tokens)},
      {"sequence.first_token_step", std::to_string(sequence.first_token_step)},
      {"sequence.completion_step", std::to_string(sequence.completion_step)},
      {"sequence.kv_blocks", std::to_string(sequence.kv_blocks)},
      {"sequence.prefix_cache_hit",
       sequence.prefix_cache_hit ? "true" : "false"},
      {"scheduler.total_steps", std::to_string(run.stats.total_steps)},
      {"scheduler.prefill_steps", std::to_string(run.stats.prefill_steps)},
      {"scheduler.decode_steps", std::to_string(run.stats.decode_steps)},
      {"scheduler.total_prompt_tokens",
       std::to_string(run.stats.total_prompt_tokens)},
      {"scheduler.total_generated_tokens",
       std::to_string(run.stats.total_generated_tokens)},
      {"scheduler.average_decode_batch_size",
       FixedDouble(run.stats.average_decode_batch_size, 2)},
      {"kv_cache.total_blocks", std::to_string(kv.total_blocks)},
      {"kv_cache.used_blocks", std::to_string(kv.used_blocks)},
      {"kv_cache.high_watermark_blocks",
       std::to_string(kv.high_watermark_blocks)},
      {"kv_cache.cached_prefixes", std::to_string(kv.cached_prefixes)},
      {"kv_cache.prefix_cache_hits",
       std::to_string(kv.prefix_cache_hits)},
      {"kv_cache.prefix_cache_misses",
       std::to_string(kv.prefix_cache_misses)},
      {"kv_cache.evictions", std::to_string(kv.evictions)},
      {"kv_cache.allocation_failures",
       std::to_string(kv.allocation_failures)},
  };
}

BackendResult ContinuousTinyLLMBackend::ProcessBatch(
    const std::vector<BackendRequest>& requests,
    const std::function<bool()>& is_cancelled) {
  if (is_cancelled()) {
    return CancelledResult();
  }

  ContinuousBatcherConfig scheduler_config;
  scheduler_config.max_prefill_tokens_per_step =
      max_prefill_tokens_per_step_;
  scheduler_config.max_decode_sequences_per_step =
      max_decode_sequences_per_step_;
  scheduler_config.kv_cache_blocks = kv_cache_blocks_;
  scheduler_config.kv_block_tokens = kv_block_tokens_;
  ContinuousBatcher scheduler(scheduler_config);

  std::vector<SequenceRequest> sequence_requests;
  sequence_requests.reserve(requests.size());
  for (const auto& request : requests) {
    const std::vector<int> token_ids = TokenizeForTinyLLM(request.prompt);
    SequenceRequest sequence;
    sequence.id = request.id;
    sequence.prompt_tokens = static_cast<int>(token_ids.size());
    sequence.max_generated_tokens = max_generated_tokens_;
    sequence.prefix_cache_key = PrefixCacheKey(token_ids, prefix_cache_tokens_);
    sequence.prefix_cache_tokens =
        sequence.prefix_cache_key.empty() ? -1 : prefix_cache_tokens_;
    sequence_requests.push_back(std::move(sequence));
  }

  const ContinuousBatchingRun run =
      scheduler.Run(sequence_requests, is_cancelled);
  for (const auto& sequence : run.results) {
    if (sequence.status == SequenceStatus::kCancelled) {
      return CancelledResult();
    }
  }

  BackendResult result;
  result.outputs.reserve(requests.size());
  result.output_metadata.reserve(requests.size());

  for (size_t index = 0; index < requests.size(); ++index) {
    const SequenceResult& sequence = run.results[index];
    if (sequence.status == SequenceStatus::kRejected) {
      result.code = BackendStatusCode::kUnavailable;
      result.message = sequence.message;
      return result;
    }

    const TinyLLMGeneration generation = GenerateTinyLLMCompletion(
        requests[index].prompt, max_generated_tokens_, temperature_, top_k_,
        is_cancelled);
    if (is_cancelled() ||
        generation.generated_tokens != max_generated_tokens_) {
      return CancelledResult();
    }

    result.outputs.push_back(FormatContinuousTinyLLMOutput(
        generation.generated, generation.prompt_tokens,
        generation.generated_tokens, generation.prefill_micros,
        generation.decode_micros, static_cast<int>(requests.size()),
        max_generated_tokens_, top_k_, temperature_,
        sequence.first_token_step, sequence.completion_step));
    result.output_metadata.push_back(
        BuildContinuousTinyLLMMetadata(run, sequence));
  }
  return result;
}

struct OnnxRuntimeBackend::Impl {
  explicit Impl(std::string model_path_value, std::string runtime_library_path_value)
      : model_path(std::move(model_path_value)),
        runtime_library_path(std::move(runtime_library_path_value)) {
    Initialize();
  }

  ~Impl() {
    if (api != nullptr) {
      if (session != nullptr) {
        api->ReleaseSession(session);
      }
      if (session_options != nullptr) {
        api->ReleaseSessionOptions(session_options);
      }
      if (memory_info != nullptr) {
        api->ReleaseMemoryInfo(memory_info);
      }
      if (env != nullptr) {
        api->ReleaseEnv(env);
      }
    }
    if (library_handle != nullptr) {
      dlclose(library_handle);
    }
  }

  bool CheckStatus(OrtStatus* status, const std::string& operation) {
    if (status == nullptr) {
      return true;
    }
    const char* error = api != nullptr ? api->GetErrorMessage(status) : "";
    unavailable_reason = operation + " failed";
    if (error != nullptr && std::string(error).size() > 0) {
      unavailable_reason += ": ";
      unavailable_reason += error;
    }
    if (api != nullptr) {
      api->ReleaseStatus(status);
    }
    ready = false;
    return false;
  }

  void Initialize() {
    if (runtime_library_path.empty()) {
      runtime_library_path = DefaultOnnxRuntimeLibraryPath();
    }
    if (!std::filesystem::exists(runtime_library_path)) {
      unavailable_reason = "libonnxruntime not found at " + runtime_library_path;
      return;
    }
    if (!std::filesystem::exists(model_path)) {
      unavailable_reason = "ONNX model not found at " + model_path;
      return;
    }

    library_handle = dlopen(runtime_library_path.c_str(), RTLD_NOW | RTLD_LOCAL);
    if (library_handle == nullptr) {
      const char* error = dlerror();
      unavailable_reason = "failed to load libonnxruntime";
      if (error != nullptr) {
        unavailable_reason += ": ";
        unavailable_reason += error;
      }
      return;
    }

    using OrtGetApiBaseFn = const OrtApiBase* (*)();
    auto* symbol = dlsym(library_handle, "OrtGetApiBase");
    if (symbol == nullptr) {
      unavailable_reason = "OrtGetApiBase symbol not found in " + runtime_library_path;
      return;
    }
    auto* get_api_base = reinterpret_cast<OrtGetApiBaseFn>(symbol);
    const OrtApiBase* api_base = get_api_base();
    if (api_base == nullptr) {
      unavailable_reason = "OrtGetApiBase returned null";
      return;
    }
    api = api_base->GetApi(ORT_API_VERSION);
    if (api == nullptr) {
      unavailable_reason = "ONNX Runtime API version is unavailable";
      return;
    }

    if (!CheckStatus(api->CreateEnv(ORT_LOGGING_LEVEL_WARNING, "laminar-onnx",
                                    &env),
                     "CreateEnv")) {
      return;
    }
    if (!CheckStatus(api->CreateSessionOptions(&session_options),
                     "CreateSessionOptions")) {
      return;
    }
    if (!CheckStatus(api->SetIntraOpNumThreads(session_options, 1),
                     "SetIntraOpNumThreads")) {
      return;
    }
    if (!CheckStatus(api->SetInterOpNumThreads(session_options, 1),
                     "SetInterOpNumThreads")) {
      return;
    }
    if (!CheckStatus(api->CreateCpuMemoryInfo(OrtArenaAllocator,
                                              OrtMemTypeDefault, &memory_info),
                     "CreateCpuMemoryInfo")) {
      return;
    }
    if (!CheckStatus(api->CreateSession(env, model_path.c_str(), session_options,
                                        &session),
                     "CreateSession")) {
      return;
    }
    ready = true;
  }

  BackendResult Run(const std::vector<BackendRequest>& requests,
                    const std::function<bool()>& is_cancelled) {
    if (!ready) {
      return UnavailableResult(unavailable_reason);
    }
    if (is_cancelled()) {
      return CancelledResult();
    }

    constexpr int kModelBatch = 3;
    constexpr int kFeatureCount = 2;
    BackendResult result;
    result.outputs.reserve(requests.size());

    const char* input_names[] = {"float_input"};
    const char* output_names[] = {"label"};
    const int64_t input_shape[] = {kModelBatch, kFeatureCount};

    for (size_t offset = 0; offset < requests.size(); offset += kModelBatch) {
      if (is_cancelled()) {
        return CancelledResult();
      }

      const size_t chunk_size = std::min<size_t>(kModelBatch, requests.size() - offset);
      std::array<float, kModelBatch * kFeatureCount> input{};
      for (size_t i = 0; i < chunk_size; ++i) {
        const auto features = IrisLikeFeatures(requests[offset + i].prompt);
        input[i * kFeatureCount] = features[0];
        input[i * kFeatureCount + 1] = features[1];
      }

      OrtValue* input_value = nullptr;
      if (!CheckStatus(api->CreateTensorWithDataAsOrtValue(
                           memory_info, input.data(),
                           input.size() * sizeof(float), input_shape, 2,
                           ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT, &input_value),
                       "CreateTensorWithDataAsOrtValue")) {
        return UnavailableResult(unavailable_reason);
      }

      const OrtValue* input_values[] = {input_value};
      OrtValue* output_value = nullptr;
      OrtStatus* run_status = api->Run(session, nullptr, input_names,
                                       input_values, 1, output_names, 1,
                                       &output_value);
      api->ReleaseValue(input_value);
      if (!CheckStatus(run_status, "Run")) {
        return UnavailableResult(unavailable_reason);
      }

      void* output_data = nullptr;
      if (!CheckStatus(api->GetTensorMutableData(output_value, &output_data),
                       "GetTensorMutableData")) {
        api->ReleaseValue(output_value);
        return UnavailableResult(unavailable_reason);
      }
      const int64_t* labels = static_cast<const int64_t*>(output_data);
      for (size_t i = 0; i < chunk_size; ++i) {
        std::ostringstream out;
        out << "OnnxRuntime(backend=onnx, model=logreg_iris, label="
            << labels[i] << ", batch_size=" << requests.size()
            << ", model_batch=3)";
        result.outputs.push_back(out.str());
      }
      api->ReleaseValue(output_value);
    }

    return result;
  }

  std::string model_path;
  std::string runtime_library_path;
  std::string unavailable_reason = "onnx runtime is not initialized";
  bool ready = false;

  void* library_handle = nullptr;
  const OrtApi* api = nullptr;
  OrtEnv* env = nullptr;
  OrtSessionOptions* session_options = nullptr;
  OrtMemoryInfo* memory_info = nullptr;
  OrtSession* session = nullptr;
};

OnnxRuntimeBackend::OnnxRuntimeBackend(std::string model_path,
                                       std::string runtime_library_path)
    : impl_(std::make_unique<Impl>(std::move(model_path),
                                   std::move(runtime_library_path))) {}

OnnxRuntimeBackend::~OnnxRuntimeBackend() = default;

std::string OnnxRuntimeBackend::Name() const { return "onnx"; }

BackendResult OnnxRuntimeBackend::ProcessBatch(
    const std::vector<BackendRequest>& requests,
    const std::function<bool()>& is_cancelled) {
  return impl_->Run(requests, is_cancelled);
}

LlamaCppBackend::LlamaCppBackend(std::string binary_path,
                                 std::string model_path, int max_tokens,
                                 int context_size, int threads,
                                 float temperature, int timeout_ms)
    : binary_path_(std::move(binary_path)),
      model_path_(std::move(model_path)),
      max_tokens_(std::clamp(max_tokens, 1, 4096)),
      context_size_(std::clamp(context_size, 128, 262144)),
      threads_(std::clamp(threads, 0, 1024)),
      temperature_(std::clamp(temperature, 0.0f, 2.0f)),
      timeout_ms_(std::clamp(timeout_ms, 100, 10 * 60 * 1000)) {}

std::string LlamaCppBackend::Name() const { return "llama_cpp"; }

BackendResult LlamaCppBackend::ProcessBatch(
    const std::vector<BackendRequest>& requests,
    const std::function<bool()>& is_cancelled) {
  if (is_cancelled()) {
    return CancelledResult();
  }
  if (model_path_.empty()) {
    return UnavailableResult(
        "LAMINAR_LLAMA_CPP_MODEL is required for llama_cpp backend");
  }
  if (!std::filesystem::exists(model_path_)) {
    return UnavailableResult("llama.cpp model not found at " + model_path_);
  }
  if (ContainsPathSeparator(binary_path_) && !IsExecutableFile(binary_path_)) {
    return UnavailableResult("llama.cpp binary is not executable at " +
                             binary_path_);
  }

  BackendResult result;
  result.outputs.reserve(requests.size());

  for (const auto& request : requests) {
    if (is_cancelled()) {
      return CancelledResult();
    }

    const auto command =
        BuildLlamaCppCommand(binary_path_, model_path_, request.prompt,
                             max_tokens_, context_size_, threads_,
                             temperature_);
    const auto started = std::chrono::steady_clock::now();
    ProcessResult process = RunProcess(
        command, std::chrono::milliseconds(timeout_ms_), is_cancelled);
    const auto elapsed = std::chrono::steady_clock::now() - started;

    if (process.cancelled) {
      return CancelledResult();
    }
    if (process.timed_out) {
      return UnavailableResult(
          "llama.cpp command timed out after " +
          std::to_string(timeout_ms_) + "ms");
    }
    if (process.launch_failed) {
      return UnavailableResult("failed to launch llama.cpp command: " +
                               process.error);
    }
    if (process.exit_code != 0) {
      std::string message =
          "llama.cpp command exited with code " +
          std::to_string(process.exit_code);
      if (!process.output.empty()) {
        message += ": " + CollapseWhitespace(process.output);
      }
      return UnavailableResult(message);
    }

    const std::string generated = CollapseWhitespace(process.output);
    result.outputs.push_back(FormatLlamaCppOutput(
        generated, model_path_, static_cast<int>(request.prompt.size()),
        static_cast<int>(generated.size()),
        std::chrono::duration_cast<std::chrono::microseconds>(elapsed).count(),
        static_cast<int>(requests.size()), max_tokens_, context_size_,
        threads_, temperature_));
  }

  return result;
}

LlamaServerBackend::LlamaServerBackend(std::string server_url, int max_tokens,
                                       float temperature, int timeout_ms,
                                       bool cache_prompt, bool stream,
                                       bool timings_per_token)
    : server_url_(std::move(server_url)),
      max_tokens_(std::clamp(max_tokens, 1, 4096)),
      temperature_(std::clamp(temperature, 0.0f, 2.0f)),
      timeout_ms_(std::clamp(timeout_ms, 100, 10 * 60 * 1000)),
      cache_prompt_(cache_prompt),
      stream_(stream),
      timings_per_token_(timings_per_token) {}

std::string LlamaServerBackend::Name() const { return "llama_server"; }

BackendResult LlamaServerBackend::ProcessBatch(
    const std::vector<BackendRequest>& requests,
    const std::function<bool()>& is_cancelled) {
  if (is_cancelled()) {
    return CancelledResult();
  }

  const ParsedHttpUrl parsed_url = ParseHttpUrl(server_url_);
  if (!parsed_url.valid) {
    return UnavailableResult(
        "LAMINAR_LLAMA_SERVER_URL must be an http:// URL");
  }

  BackendResult result;
  result.outputs.reserve(requests.size());
  result.output_metadata.reserve(requests.size());

  for (const auto& request : requests) {
    if (is_cancelled()) {
      return CancelledResult();
    }

    const std::string body =
        BuildLlamaServerRequestBody(request.prompt, max_tokens_,
                                    temperature_, cache_prompt_, stream_,
                                    timings_per_token_);
    const auto started = std::chrono::steady_clock::now();
    const HttpResponse response = HttpPostJson(
        parsed_url, body, timeout_ms_, is_cancelled);
    const auto elapsed = std::chrono::steady_clock::now() - started;

    if (is_cancelled() || response.error == "cancelled") {
      return CancelledResult();
    }
    if (!response.ok) {
      std::string message = response.error.empty()
                                ? "llama server request failed"
                                : response.error;
      if (!response.body.empty()) {
        message += ": " + CollapseWhitespace(response.body);
      }
      return UnavailableResult(message);
    }

    const LlamaServerCompletion completion =
        ParseLlamaServerCompletion(response.body, stream_);
    if (completion.content.empty()) {
      return UnavailableResult(
          "llama server response did not include a content field");
    }
    const std::string generated = CollapseWhitespace(completion.content);
    const long long duration_micros =
        std::chrono::duration_cast<std::chrono::microseconds>(elapsed).count();
    const long long ttft_micros = response.first_response_byte_micros;
    result.outputs.push_back(FormatLlamaServerOutput(
        generated, EndpointForOutput(server_url_),
        static_cast<int>(request.prompt.size()),
        static_cast<int>(generated.size()), duration_micros,
        static_cast<int>(requests.size()), max_tokens_, temperature_,
        cache_prompt_, stream_, timings_per_token_,
        completion.stream_chunks, ttft_micros, completion.tokens_predicted,
        completion.tokens_evaluated));

    result.output_metadata.push_back(BuildLlamaServerMetadata(
        stream_, cache_prompt_, timings_per_token_, completion,
        request.prompt.size(), generated.size(), ttft_micros));
  }

  return result;
}

BackendResult LlamaServerBackend::ProcessStream(
    const BackendRequest& request, const std::function<bool()>& is_cancelled,
    const BackendStreamCallback& emit) {
  if (!stream_) {
    return InferenceBackend::ProcessStream(request, is_cancelled, emit);
  }
  if (is_cancelled()) {
    return CancelledResult();
  }

  const ParsedHttpUrl parsed_url = ParseHttpUrl(server_url_);
  if (!parsed_url.valid) {
    return UnavailableResult(
        "LAMINAR_LLAMA_SERVER_URL must be an http:// URL");
  }

  const std::string body =
      BuildLlamaServerRequestBody(request.prompt, max_tokens_, temperature_,
                                  cache_prompt_, /*stream=*/true,
                                  timings_per_token_);
  const auto started = std::chrono::steady_clock::now();
  LlamaServerCompletion completion;
  std::string pending_line;
  int token_index = 0;

  auto consume_line = [&](std::string line) {
    line = TrimWhitespace(std::move(line));
    if (line.rfind("data:", 0) != 0) {
      return true;
    }
    const std::string data = TrimWhitespace(line.substr(std::strlen("data:")));
    if (data.empty()) {
      return true;
    }
    const LlamaServerChunk chunk = ParseLlamaServerChunk(data);
    if (chunk.done) {
      return true;
    }

    ++completion.stream_chunks;
    if (chunk.tokens_predicted >= 0) {
      completion.tokens_predicted = chunk.tokens_predicted;
    }
    if (chunk.tokens_evaluated >= 0) {
      completion.tokens_evaluated = chunk.tokens_evaluated;
    }
    if (!chunk.has_content) {
      return true;
    }

    completion.content += chunk.content;
    BackendStreamEvent event;
    event.event_type = "token";
    event.delta = chunk.content;
    event.metadata = {
        {"stream", "true"},
        {"token_index", std::to_string(token_index)},
        {"delta_chars", std::to_string(chunk.content.size())},
        {"stream_chunks", std::to_string(completion.stream_chunks)},
        {"tokens_predicted", std::to_string(completion.tokens_predicted)},
        {"tokens_evaluated", std::to_string(completion.tokens_evaluated)},
    };
    ++token_index;
    return emit(event);
  };

  auto on_body = [&](const char* data, size_t size) {
    pending_line.append(data, size);
    while (true) {
      const size_t newline = pending_line.find('\n');
      if (newline == std::string::npos) {
        return true;
      }
      std::string line = pending_line.substr(0, newline);
      pending_line.erase(0, newline + 1);
      if (!consume_line(std::move(line))) {
        return false;
      }
    }
  };

  const HttpResponse response = HttpPostJsonStream(
      parsed_url, body, timeout_ms_, is_cancelled, on_body);
  const auto elapsed = std::chrono::steady_clock::now() - started;

  if (is_cancelled() || response.error == "cancelled") {
    return CancelledResult();
  }
  if (!response.ok) {
    std::string message = response.error.empty()
                              ? "llama server request failed"
                              : response.error;
    if (!response.body.empty()) {
      message += ": " + CollapseWhitespace(response.body);
    }
    return UnavailableResult(message);
  }
  if (!pending_line.empty() && !consume_line(std::move(pending_line))) {
    return CancelledResult();
  }
  if (completion.content.empty()) {
    return UnavailableResult(
        "llama server response did not include a content field");
  }

  const std::string generated = CollapseWhitespace(completion.content);
  const long long duration_micros =
      std::chrono::duration_cast<std::chrono::microseconds>(elapsed).count();
  const long long ttft_micros = response.first_response_byte_micros;
  const auto metadata = BuildLlamaServerMetadata(
      /*stream=*/true, cache_prompt_, timings_per_token_, completion,
      request.prompt.size(), generated.size(), ttft_micros);

  BackendResult result;
  result.outputs.push_back(FormatLlamaServerOutput(
      generated, EndpointForOutput(server_url_),
      static_cast<int>(request.prompt.size()),
      static_cast<int>(generated.size()), duration_micros,
      /*batch_size=*/1, max_tokens_, temperature_, cache_prompt_,
      /*stream=*/true, timings_per_token_, completion.stream_chunks,
      ttft_micros, completion.tokens_predicted, completion.tokens_evaluated));
  result.output_metadata.push_back(metadata);

  BackendStreamEvent done;
  done.event_type = "done";
  done.result = result.outputs[0];
  done.metadata = metadata;
  if (!emit(done)) {
    return CancelledResult();
  }
  return result;
}

BackendKind ParseBackendKind(const std::string& raw) {
  const std::string normalized = TrimLower(raw);
  if (normalized == "tiny_model" || normalized == "tiny-text" ||
      normalized == "tiny_text" || normalized == "tiny_text_model") {
    return BackendKind::kTinyTextModel;
  }
  if (normalized == "tiny_llm" || normalized == "tiny-llm" ||
      normalized == "tiny_decoder" || normalized == "tiny_decoder_llm") {
    return BackendKind::kTinyLLM;
  }
  if (normalized == "continuous_tiny_llm" ||
      normalized == "continuous-tiny-llm" ||
      normalized == "continuous_llm" || normalized == "continuous-llm") {
    return BackendKind::kContinuousTinyLLM;
  }
  if (normalized == "llama_cpp" || normalized == "llama-cpp" ||
      normalized == "llama" || normalized == "gguf") {
    return BackendKind::kLlamaCpp;
  }
  if (normalized == "llama_server" || normalized == "llama-server" ||
      normalized == "llama.cpp-server" || normalized == "llama_server_http") {
    return BackendKind::kLlamaServer;
  }
  if (normalized == "onnx" || normalized == "onnxruntime" ||
      normalized == "onnx_runtime") {
    return BackendKind::kOnnxRuntime;
  }
  return BackendKind::kSimulator;
}

std::string BackendKindName(BackendKind kind) {
  switch (kind) {
    case BackendKind::kLlamaServer:
      return "llama_server";
    case BackendKind::kLlamaCpp:
      return "llama_cpp";
    case BackendKind::kOnnxRuntime:
      return "onnx";
    case BackendKind::kContinuousTinyLLM:
      return "continuous_tiny_llm";
    case BackendKind::kTinyLLM:
      return "tiny_llm";
    case BackendKind::kTinyTextModel:
      return "tiny_text_model";
    case BackendKind::kSimulator:
    default:
      return "simulator";
  }
}

BackendConfig BackendConfigFromEnv() {
  BackendConfig config;
  const char* backend = std::getenv("LAMINAR_WORKER_BACKEND");
  if (backend != nullptr) {
    config.kind = ParseBackendKind(backend);
  }
  config.simulator_base_latency_ms =
      EnvInt("LAMINAR_SIMULATOR_BASE_LATENCY_MS",
             config.simulator_base_latency_ms, 0, 60000);
  config.simulator_per_request_latency_ms =
      EnvInt("LAMINAR_SIMULATOR_PER_REQUEST_LATENCY_MS",
             config.simulator_per_request_latency_ms, 0, 60000);
  config.tiny_model_iterations =
      EnvInt("LAMINAR_TINY_MODEL_ITERATIONS", config.tiny_model_iterations, 1,
             1024);
  config.tiny_llm_max_generated_tokens =
      EnvInt("LAMINAR_TINY_LLM_MAX_GENERATED_TOKENS",
             config.tiny_llm_max_generated_tokens, 1, 64);
  config.tiny_llm_top_k =
      EnvInt("LAMINAR_TINY_LLM_TOP_K", config.tiny_llm_top_k, 1,
             kTinyLLMVocabSize - 2);
  config.tiny_llm_temperature =
      EnvFloat("LAMINAR_TINY_LLM_TEMPERATURE",
               config.tiny_llm_temperature, 0.05f, 2.0f);
  config.continuous_max_prefill_tokens_per_step =
      EnvInt("LAMINAR_CONTINUOUS_MAX_PREFILL_TOKENS_PER_STEP",
             config.continuous_max_prefill_tokens_per_step, 1, 1 << 20);
  config.continuous_max_decode_sequences_per_step =
      EnvInt("LAMINAR_CONTINUOUS_MAX_DECODE_SEQUENCES_PER_STEP",
             config.continuous_max_decode_sequences_per_step, 1, 1 << 20);
  config.continuous_kv_cache_blocks =
      EnvInt("LAMINAR_CONTINUOUS_KV_CACHE_BLOCKS",
             config.continuous_kv_cache_blocks, 1, 1 << 20);
  config.continuous_kv_block_tokens =
      EnvInt("LAMINAR_CONTINUOUS_KV_BLOCK_TOKENS",
             config.continuous_kv_block_tokens, 1, 1 << 20);
  config.continuous_prefix_cache_tokens =
      EnvInt("LAMINAR_CONTINUOUS_PREFIX_CACHE_TOKENS",
             config.continuous_prefix_cache_tokens, 0, 1024);
  config.onnx_model_path =
      EnvString("LAMINAR_ONNX_MODEL_PATH", config.onnx_model_path);
  config.onnx_runtime_library_path =
      EnvString("LAMINAR_ONNX_RUNTIME_LIB", DefaultOnnxRuntimeLibraryPath());
  config.llama_cpp_binary_path =
      EnvString("LAMINAR_LLAMA_CPP_BINARY", config.llama_cpp_binary_path);
  config.llama_cpp_model_path =
      EnvString("LAMINAR_LLAMA_CPP_MODEL", config.llama_cpp_model_path);
  config.llama_cpp_max_tokens =
      EnvInt("LAMINAR_LLAMA_CPP_MAX_TOKENS", config.llama_cpp_max_tokens, 1,
             4096);
  config.llama_cpp_context_size =
      EnvInt("LAMINAR_LLAMA_CPP_CONTEXT_SIZE", config.llama_cpp_context_size,
             128, 262144);
  config.llama_cpp_threads =
      EnvInt("LAMINAR_LLAMA_CPP_THREADS", config.llama_cpp_threads, 0, 1024);
  config.llama_cpp_temperature =
      EnvFloat("LAMINAR_LLAMA_CPP_TEMPERATURE",
               config.llama_cpp_temperature, 0.0f, 2.0f);
  config.llama_cpp_timeout_ms =
      EnvInt("LAMINAR_LLAMA_CPP_TIMEOUT_MS", config.llama_cpp_timeout_ms, 100,
             10 * 60 * 1000);
  config.llama_server_url =
      EnvString("LAMINAR_LLAMA_SERVER_URL", config.llama_server_url);
  config.llama_server_max_tokens =
      EnvInt("LAMINAR_LLAMA_SERVER_MAX_TOKENS",
             config.llama_server_max_tokens, 1, 4096);
  config.llama_server_temperature =
      EnvFloat("LAMINAR_LLAMA_SERVER_TEMPERATURE",
               config.llama_server_temperature, 0.0f, 2.0f);
  config.llama_server_timeout_ms =
      EnvInt("LAMINAR_LLAMA_SERVER_TIMEOUT_MS",
             config.llama_server_timeout_ms, 100, 10 * 60 * 1000);
  config.llama_server_cache_prompt =
      EnvBool("LAMINAR_LLAMA_SERVER_CACHE_PROMPT",
              config.llama_server_cache_prompt);
  config.llama_server_stream =
      EnvBool("LAMINAR_LLAMA_SERVER_STREAM", config.llama_server_stream);
  config.llama_server_timings_per_token =
      EnvBool("LAMINAR_LLAMA_SERVER_TIMINGS_PER_TOKEN",
              config.llama_server_timings_per_token);
  return config;
}

std::unique_ptr<InferenceBackend> CreateBackend(const BackendConfig& config) {
  if (config.kind == BackendKind::kLlamaServer) {
    return std::make_unique<LlamaServerBackend>(
        config.llama_server_url, config.llama_server_max_tokens,
        config.llama_server_temperature, config.llama_server_timeout_ms,
        config.llama_server_cache_prompt, config.llama_server_stream,
        config.llama_server_timings_per_token);
  }
  if (config.kind == BackendKind::kLlamaCpp) {
    return std::make_unique<LlamaCppBackend>(
        config.llama_cpp_binary_path, config.llama_cpp_model_path,
        config.llama_cpp_max_tokens, config.llama_cpp_context_size,
        config.llama_cpp_threads, config.llama_cpp_temperature,
        config.llama_cpp_timeout_ms);
  }
  if (config.kind == BackendKind::kOnnxRuntime) {
    return std::make_unique<OnnxRuntimeBackend>(
        config.onnx_model_path, config.onnx_runtime_library_path);
  }
  if (config.kind == BackendKind::kContinuousTinyLLM) {
    return std::make_unique<ContinuousTinyLLMBackend>(
        config.tiny_llm_max_generated_tokens,
        config.tiny_llm_temperature, config.tiny_llm_top_k,
        config.continuous_max_prefill_tokens_per_step,
        config.continuous_max_decode_sequences_per_step,
        config.continuous_kv_cache_blocks,
        config.continuous_kv_block_tokens,
        config.continuous_prefix_cache_tokens);
  }
  if (config.kind == BackendKind::kTinyLLM) {
    return std::make_unique<TinyLLMBackend>(
        config.tiny_llm_max_generated_tokens,
        config.tiny_llm_temperature, config.tiny_llm_top_k);
  }
  if (config.kind == BackendKind::kTinyTextModel) {
    return std::make_unique<TinyTextModelBackend>(config.tiny_model_iterations);
  }
  return std::make_unique<SimulatorBackend>(
      config.simulator_base_latency_ms,
      config.simulator_per_request_latency_ms);
}
