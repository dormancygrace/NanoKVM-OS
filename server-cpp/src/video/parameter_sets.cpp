#include "video/parameter_sets.hpp"

#include <algorithm>
#include <cstring>
#include <functional>
#include <optional>
#include <string_view>

namespace nkos::video {

namespace {

using Bytes = std::vector<unsigned char>;

constexpr int hevc_vps = 32, hevc_sps = 33, h264_sps = 7;
constexpr unsigned video_format_unspecified = 5, colour_bt709 = 1;

// The offset of the next 00 00 01 at or after from, or npos.
std::size_t start_code(const unsigned char *data, std::size_t size, std::size_t from)
{
	for (std::size_t i = from; i + 2 < size; i++) {
		if (data[i + 2] > 1) {
			i += 2;
			continue;
		}
		if (data[i] == 0 && data[i + 1] == 0 && data[i + 2] == 1) return i;
	}
	return std::string_view::npos;
}

Bytes unescape(const unsigned char *b, std::size_t n)
{
	Bytes out;
	out.reserve(n);
	int zeros = 0;
	for (std::size_t i = 0; i < n; i++) {
		unsigned char x = b[i];
		if (zeros >= 2 && x == 3) {
			zeros = 0;
			continue;
		}
		out.push_back(x);
		zeros = x == 0 ? zeros + 1 : 0;
	}
	return out;
}

Bytes escape(const Bytes &b)
{
	Bytes out;
	out.reserve(b.size() + b.size() / 64 + 1);
	int zeros = 0;
	for (unsigned char x : b) {
		if (zeros >= 2 && x <= 3) {
			out.push_back(3);
			zeros = 0;
		}
		out.push_back(x);
		zeros = x == 0 ? zeros + 1 : 0;
	}
	return out;
}

// The bit position of rbsp_stop_one_bit, or -1.
long last_set_bit(const Bytes &b)
{
	for (long i = static_cast<long>(b.size()) - 1; i >= 0; i--)
		if (b[static_cast<std::size_t>(i)] != 0)
			for (int bit = 0; bit < 8; bit++)
				if (b[static_cast<std::size_t>(i)] & (1 << bit)) return i * 8 + 7 - bit;
	return -1;
}

struct BitReader {
	const Bytes &data;
	long pos = 0;
	bool failed = false;

	unsigned bit()
	{
		if (pos >= static_cast<long>(data.size()) * 8) {
			failed = true;
			return 0;
		}
		unsigned v = data[static_cast<std::size_t>(pos / 8)] >> (7 - pos % 8) & 1;
		pos++;
		return v;
	}
	std::uint32_t bits(int n)
	{
		std::uint32_t v = 0;
		for (int i = 0; i < n; i++) v = v << 1 | bit();
		return v;
	}
	void skip(long n)
	{
		pos += n;
		if (pos > static_cast<long>(data.size()) * 8) failed = true;
	}
	std::uint32_t ue()
	{
		int zeros = 0;
		while (bit() == 0) {
			if (failed || zeros == 31) {
				failed = true;
				return 0;
			}
			zeros++;
		}
		return ((1u << zeros) - 1) + bits(zeros);
	}
	std::int32_t se()
	{
		std::uint32_t v = ue();
		if (v & 1) return static_cast<std::int32_t>((v + 1) / 2);
		return -static_cast<std::int32_t>(v / 2);
	}
};

struct BitWriter {
	Bytes data;
	long n = 0;

	void put(std::uint32_t v, int count)
	{
		for (int i = count - 1; i >= 0; i--) {
			if (n % 8 == 0) data.push_back(0);
			if (v >> i & 1) data[static_cast<std::size_t>(n / 8)] |= static_cast<unsigned char>(1 << (7 - n % 8));
			n++;
		}
	}
	void ue(std::uint32_t v)
	{
		v++;
		int len = 0;
		for (std::uint32_t x = v; x > 1; x >>= 1) len++;
		put(0, len);
		put(v, len + 1);
	}
	void copy_bits(const Bytes &src, long from, long to)
	{
		// Never past the source: a reader that ran off its end reports that itself (failed).
		to = std::min(to, static_cast<long>(src.size()) * 8);
		for (long i = from; i < to; i++) put(src[static_cast<std::size_t>(i / 8)] >> (7 - i % 8) & 1, 1);
	}
};

void skip_profile_tier_level(BitReader &r, std::uint32_t max_sub_layers_minus1)
{
	r.skip(88 + 8);  // general profile, tier and constraint flags; general_level_idc
	std::vector<bool> profile(max_sub_layers_minus1), level(max_sub_layers_minus1);
	for (std::uint32_t i = 0; i < max_sub_layers_minus1; i++) {
		profile[i] = r.bits(1) == 1;
		level[i] = r.bits(1) == 1;
	}
	if (max_sub_layers_minus1 > 0) r.skip(static_cast<long>(8 - max_sub_layers_minus1) * 2);  // reserved_zero_2bits
	for (std::uint32_t i = 0; i < max_sub_layers_minus1; i++) {
		if (profile[i]) r.skip(88);
		if (level[i]) r.skip(8);
	}
}

// The NAL unit with sub_layer_ordering_info_present_flag = 0, or nullopt when it signals a single set already, has
// one sub-layer or cannot be parsed.
std::optional<Bytes> hevc_single_ordering_set(const unsigned char *nal, std::size_t size, int type)
{
	Bytes rbsp = unescape(nal + 2, size - 2);
	BitReader r{rbsp};
	std::uint32_t max_sub_layers_minus1;
	if (type == hevc_vps) {
		r.skip(4 + 1 + 1 + 6);  // id, base layer flags, max_layers_minus1
		max_sub_layers_minus1 = r.bits(3);
		r.skip(1 + 16);  // temporal_id_nesting, reserved 0xffff
	} else {
		r.skip(4);  // sps_video_parameter_set_id
		max_sub_layers_minus1 = r.bits(3);
		r.skip(1);  // temporal_id_nesting
	}
	skip_profile_tier_level(r, max_sub_layers_minus1);
	if (type == hevc_sps) {
		r.ue();                       // sps_seq_parameter_set_id
		if (r.ue() == 3) r.skip(1);   // chroma_format_idc, separate_colour_plane_flag
		r.ue();                       // pic_width_in_luma_samples
		r.ue();                       // pic_height_in_luma_samples
		if (r.bits(1) == 1) r.ue(), r.ue(), r.ue(), r.ue();  // conformance window
		r.ue();                       // bit_depth_luma_minus8
		r.ue();                       // bit_depth_chroma_minus8
		r.ue();                       // log2_max_pic_order_cnt_lsb_minus4
	}
	long flag_at = r.pos;
	if (max_sub_layers_minus1 == 0 || r.bits(1) == 0 || r.failed) return std::nullopt;
	std::uint32_t dpb = 0, reorder = 0, latency = 0;
	for (std::uint32_t i = 0; i <= max_sub_layers_minus1; i++) {
		// The spec requires both to be non-decreasing; the maxima keep a stream that breaks the rule safe.
		dpb = std::max(dpb, r.ue());
		reorder = std::max(reorder, r.ue());
		latency = r.ue();
	}
	long stop = last_set_bit(rbsp);
	if (r.failed || stop < r.pos) return std::nullopt;
	BitWriter w;
	w.copy_bits(rbsp, 0, flag_at);
	w.put(0, 1);
	w.ue(dpb);
	w.ue(reorder);
	w.ue(latency);
	w.copy_bits(rbsp, r.pos, stop);
	w.put(1, 1);  // rbsp_stop_one_bit
	Bytes out{nal[0], nal[1]};
	Bytes body = escape(w.data);
	out.insert(out.end(), body.begin(), body.end());
	return out;
}

void write_colour_description(BitWriter &w)
{
	w.put(1, 1);             // colour_description_present_flag
	w.put(colour_bt709, 8);  // colour_primaries
	w.put(colour_bt709, 8);  // transfer_characteristics
	w.put(colour_bt709, 8);  // matrix_coefficients
}

void write_video_signal_type(BitWriter &w)
{
	w.put(1, 1);                         // video_signal_type_present_flag
	w.put(video_format_unspecified, 3);  // video_format
	w.put(0, 1);                         // video_full_range_flag: 16-235
	write_colour_description(w);
}

// rbsp (after the NAL header) with a VUI that describes the colour coding. r has read up to
// vui_parameters_present_flag; minimal_vui writes the rest of a VUI that only carries the signal type.
std::optional<Bytes> with_colour(const Bytes &rbsp, BitReader &r, const std::function<void(BitWriter &)> &minimal_vui)
{
	long stop = last_set_bit(rbsp);
	if (r.failed || stop < r.pos) return std::nullopt;
	BitWriter w;
	long at = r.pos;
	if (r.bits(1) == 0) {  // vui_parameters_present_flag
		w.copy_bits(rbsp, 0, at);
		w.put(1, 1);
		w.put(0, 1);  // aspect_ratio_info_present_flag
		w.put(0, 1);  // overscan_info_present_flag
		write_video_signal_type(w);
		minimal_vui(w);
	} else {
		if (r.bits(1) == 1 && r.bits(8) == 255) r.skip(32);  // aspect ratio, EXTENDED_SAR
		if (r.bits(1) == 1) r.skip(1);                      // overscan
		at = r.pos;
		if (r.failed) return std::nullopt;
		if (r.bits(1) == 0) {  // video_signal_type_present_flag
			w.copy_bits(rbsp, 0, at);
			write_video_signal_type(w);
		} else {
			r.skip(3 + 1);  // video_format, video_full_range_flag
			at = r.pos;
			if (r.failed || r.bits(1) == 1) return std::nullopt;  // colour_description_present_flag
			w.copy_bits(rbsp, 0, at);
			write_colour_description(w);
		}
	}
	if (r.failed || stop < r.pos) return std::nullopt;
	w.copy_bits(rbsp, r.pos, stop);
	w.put(1, 1);  // rbsp_stop_one_bit
	return w.data;
}

std::optional<Bytes> h264_sps_with_colour(const unsigned char *nal, std::size_t size)
{
	Bytes rbsp = unescape(nal + 1, size - 1);
	BitReader r{rbsp};
	std::uint32_t profile = r.bits(8);
	r.skip(16);  // constraint flags, level_idc
	r.ue();      // seq_parameter_set_id
	switch (profile) {
	case 100: case 110: case 122: case 244: case 44: case 83: case 86: case 118: case 128: case 138: case 139:
	case 134: case 135:
		if (r.ue() == 3) r.skip(1);  // chroma_format_idc, separate_colour_plane_flag
		r.ue();                      // bit_depth_luma_minus8
		r.ue();                      // bit_depth_chroma_minus8
		r.skip(1);                   // qpprime_y_zero_transform_bypass_flag
		if (r.bits(1) == 1) return std::nullopt;  // seq_scaling_matrix_present_flag: not parsed
		break;
	default:
		break;
	}
	r.ue();  // log2_max_frame_num_minus4
	switch (r.ue()) {  // pic_order_cnt_type
	case 0:
		r.ue();
		break;
	case 1:
		r.skip(1);
		r.se();
		r.se();
		for (std::uint32_t n = r.ue(); n > 0 && !r.failed; n--) r.se();
		break;
	default:
		break;
	}
	r.ue();                         // max_num_ref_frames
	r.skip(1);                      // gaps_in_frame_num_value_allowed_flag
	r.ue();                         // pic_width_in_mbs_minus1
	r.ue();                         // pic_height_in_map_units_minus1
	if (r.bits(1) == 0) r.skip(1);  // frame_mbs_only_flag, mb_adaptive_frame_field_flag
	r.skip(1);                      // direct_8x8_inference_flag
	if (r.bits(1) == 1) r.ue(), r.ue(), r.ue(), r.ue();  // frame cropping
	auto out = with_colour(rbsp, r, [](BitWriter &w) {
		for (int i = 0; i < 6; i++) w.put(0, 1);  // chroma loc, timing, nal/vcl hrd, pic_struct, restriction
	});
	if (!out) return std::nullopt;
	Bytes nal_out{nal[0]};
	Bytes body = escape(*out);
	nal_out.insert(nal_out.end(), body.begin(), body.end());
	return nal_out;
}

// num st_ref_pic_set() structures of an SPS; delta POCs derived as in H.265 (7-61, 7-62).
bool skip_short_term_ref_pic_sets(BitReader &r, std::uint32_t num)
{
	if (num > 64) return false;
	struct Set {
		std::vector<std::int32_t> s0, s1;
	};
	std::vector<Set> sets;
	for (std::uint32_t idx = 0; idx < num && !r.failed; idx++) {
		Set set;
		if (idx > 0 && r.bits(1) == 1) {  // inter_ref_pic_set_prediction_flag
			std::int32_t sign = static_cast<std::int32_t>(r.bits(1));
			std::int32_t delta = (1 - 2 * sign) * static_cast<std::int32_t>(r.ue() + 1);
			const Set &ref = sets[idx - 1];
			std::size_t n = ref.s0.size() + ref.s1.size();
			std::vector<bool> use(n + 1);
			for (std::size_t j = 0; j < use.size(); j++) {
				bool used = r.bits(1) == 1;
				use[j] = used || r.bits(1) == 1;
			}
			for (std::size_t j = ref.s1.size(); j-- > 0;)
				if (std::int32_t d = ref.s1[j] + delta; d < 0 && use[ref.s0.size() + j]) set.s0.push_back(d);
			if (delta < 0 && use[n]) set.s0.push_back(delta);
			for (std::size_t j = 0; j < ref.s0.size(); j++)
				if (std::int32_t d = ref.s0[j] + delta; d < 0 && use[j]) set.s0.push_back(d);
			for (std::size_t j = ref.s0.size(); j-- > 0;)
				if (std::int32_t d = ref.s0[j] + delta; d > 0 && use[j]) set.s1.push_back(d);
			if (delta > 0 && use[n]) set.s1.push_back(delta);
			for (std::size_t j = 0; j < ref.s1.size(); j++)
				if (std::int32_t d = ref.s1[j] + delta; d > 0 && use[ref.s0.size() + j]) set.s1.push_back(d);
		} else {
			std::uint32_t negative = r.ue(), positive = r.ue();
			if (negative > 16 || positive > 16) return false;
			std::int32_t poc = 0;
			for (std::uint32_t i = 0; i < negative; i++) {
				poc -= static_cast<std::int32_t>(r.ue() + 1);
				r.skip(1);
				set.s0.push_back(poc);
			}
			poc = 0;
			for (std::uint32_t i = 0; i < positive; i++) {
				poc += static_cast<std::int32_t>(r.ue() + 1);
				r.skip(1);
				set.s1.push_back(poc);
			}
		}
		if (set.s0.size() + set.s1.size() > 16) return false;
		sets.push_back(std::move(set));
	}
	return !r.failed;
}

std::optional<Bytes> hevc_sps_with_colour(const Bytes &nal)
{
	Bytes rbsp = unescape(nal.data() + 2, nal.size() - 2);
	BitReader r{rbsp};
	r.skip(4);  // sps_video_parameter_set_id
	std::uint32_t max_sub_layers_minus1 = r.bits(3);
	r.skip(1);  // sps_temporal_id_nesting_flag
	skip_profile_tier_level(r, max_sub_layers_minus1);
	r.ue();                       // sps_seq_parameter_set_id
	if (r.ue() == 3) r.skip(1);   // chroma_format_idc
	r.ue();                       // pic_width_in_luma_samples
	r.ue();                       // pic_height_in_luma_samples
	if (r.bits(1) == 1) r.ue(), r.ue(), r.ue(), r.ue();  // conformance_window_flag
	r.ue();                       // bit_depth_luma_minus8
	r.ue();                       // bit_depth_chroma_minus8
	long log2_max_poc_lsb = static_cast<long>(r.ue()) + 4;
	std::uint32_t first = max_sub_layers_minus1;
	if (r.bits(1) == 1) first = 0;  // sps_sub_layer_ordering_info_present_flag
	for (std::uint32_t i = first; i <= max_sub_layers_minus1 && !r.failed; i++) r.ue(), r.ue(), r.ue();
	for (int i = 0; i < 6; i++) r.ue();  // coding block, transform block sizes and depths
	if (r.bits(1) == 1 && r.bits(1) == 1) return std::nullopt;  // scaling list data: not parsed
	r.skip(2);  // amp_enabled_flag, sample_adaptive_offset_enabled_flag
	if (r.bits(1) == 1) {  // pcm_enabled_flag
		r.skip(8);
		r.ue();
		r.ue();
		r.skip(1);
	}
	if (!skip_short_term_ref_pic_sets(r, r.ue())) return std::nullopt;
	if (r.bits(1) == 1)  // long_term_ref_pics_present_flag
		for (std::uint32_t n = r.ue(); n > 0 && !r.failed; n--) r.skip(log2_max_poc_lsb + 1);
	r.skip(2);  // sps_temporal_mvp_enabled_flag, strong_intra_smoothing_enabled_flag
	auto out = with_colour(rbsp, r, [](BitWriter &w) {
		for (int i = 0; i < 7; i++) w.put(0, 1);  // chroma loc .. bitstream_restriction_flag
	});
	if (!out) return std::nullopt;
	Bytes nal_out{nal[0], nal[1]};
	Bytes body = escape(*out);
	nal_out.insert(nal_out.end(), body.begin(), body.end());
	return nal_out;
}

bool is_hevc_slice(unsigned char header) { return (header >> 1 & 63) < hevc_vps; }

bool is_h264_slice(unsigned char header)
{
	int type = header & 31;
	return type >= 1 && type <= 5;
}

} // namespace

void fix_parameter_sets(Codec codec, bool colour, Frame &frame)
{
	if (codec == Codec::h264 && !colour) return;
	unsigned char *data = frame.data();
	const std::size_t size = frame.size;
	struct Replacement {
		std::size_t start, end;
		Bytes nal;
	};
	std::vector<Replacement> replacements;
	auto is_slice = codec == Codec::h264 ? is_h264_slice : is_hevc_slice;

	// The NAL units before the first slice; parameter sets precede it, so slice data is never searched.
	std::size_t i = start_code(data, size, 0);
	while (i != std::string_view::npos) {
		std::size_t start = i + 3;
		if (start >= size || is_slice(data[start])) break;
		std::size_t next = start_code(data, size, start);
		std::size_t end = size;
		if (next != std::string_view::npos) {
			end = next;
			while (end > start && data[end - 1] == 0) end--;  // a four-byte start code or trailing zeros
		}
		const unsigned char *nal = data + start;
		std::size_t n = end - start;
		if (codec == Codec::h264) {
			if (n >= 2 && (nal[0] & 31) == h264_sps)
				if (auto fixed = h264_sps_with_colour(nal, n)) replacements.push_back({start, end, std::move(*fixed)});
		} else if (n >= 3) {
			int type = nal[0] >> 1 & 63;
			if (type == hevc_vps || type == hevc_sps) {
				auto fixed = hevc_single_ordering_set(nal, n, type);
				bool changed = fixed.has_value();
				if (!changed) fixed = Bytes(nal, nal + n);
				if (colour && type == hevc_sps)
					if (auto described = hevc_sps_with_colour(*fixed)) fixed = std::move(described), changed = true;
				if (changed) replacements.push_back({start, end, std::move(*fixed)});
			}
		}
		i = next;
	}
	if (replacements.empty()) return;

	std::size_t prefix_end = replacements.back().end;
	Bytes prefix;
	prefix.reserve(prefix_end + 64);
	std::size_t last = 0;
	for (const auto &r : replacements) {
		prefix.insert(prefix.end(), data + last, data + r.start);
		prefix.insert(prefix.end(), r.nal.begin(), r.nal.end());
		last = r.end;
	}
	prefix.insert(prefix.end(), data + last, data + prefix_end);

	if (prefix.size() > prefix_end) {
		auto grown = frame_buffer(frame_headroom + prefix.size() + size - prefix_end);
		std::memcpy(grown->data() + frame_headroom, prefix.data(), prefix.size());
		std::memcpy(grown->data() + frame_headroom + prefix.size(), data + prefix_end, size - prefix_end);
		frame.storage = std::move(grown);
		frame.offset = frame_headroom;
		frame.size = prefix.size() + size - prefix_end;
		return;
	}
	std::size_t shift = prefix_end - prefix.size();
	std::memmove(data + shift, prefix.data(), prefix.size());
	frame.offset += shift;
	frame.size -= shift;
}

} // namespace nkos::video
