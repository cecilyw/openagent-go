// openagent-pdk — Rust PDK for building WASM plugins for openagent-go.
//
// Add to Cargo.toml:
//   [dependencies]
//   openagent-pdk = { git = "https://github.com/yusheng-g/openagent-go", path = "plugin/pdk/rust" }
//
// Quick start:
//   use openagent_pdk::prelude::*;

#![cfg_attr(not(test), no_std)]

extern crate alloc;

pub mod types;
pub mod export;

// host wrappers reference wasm imports ("host" module) that don't exist in
// host-side test builds — gate them out so cargo test can link.
#[cfg(not(test))]
pub mod host;

// ── allocator ──

// First-fit free-list allocator over a static 128 KB heap.
//
// Memory is a working set, not a lifetime accumulator: dealloc actually
// reclaims and freed blocks coalesce with their neighbours, so long-lived
// plugin processes don't exhaust the heap (the previous bump allocator
// never freed anything — every call leaked its input, output, and host
// response buffers, and a ~50-turn conversation could empty the 128 KB).
//
// Block layout (wasm32 — 32-bit words):
//
//   offset 0   [size: u32]   full block size in bytes, header included;
//                            low bit = in-use flag (set by alloc, cleared
//                            by dealloc)
//   offset 4   in use: this block's start offset, at payload-4
//              free:   next free block offset, or NONE
//   offset 8   payload — 8-aligned; payloads aligned above 8 move up and
//                        keep their block start at payload-4
//
// Free blocks form a singly linked list (first-fit). dealloc coalesces
// with the next block in O(1) (every block carries its size) and with the
// previous block via a free-list walk (O(n) — n stays tiny on 128 KB).
//
// dealloc is defensive: pointers outside the heap range, misaligned
// pointers, and double frees are silent no-ops. That makes it safe for
// the host to free any packed result — data-segment pointers (sdk_meta)
// and zero pointers are rejected by the range check.
//
// alloc returns null when the heap is exhausted; callers (sdk_return)
// check it and never write through null.

use core::alloc::{GlobalAlloc, Layout};

const HEAP_SIZE: u32 = 131_072; // 128 KB
const HEADER: u32 = 8; // size + back/next words
const MIN_BLOCK: u32 = HEADER + 8; // smallest block that can hold a payload
const IN_USE: u32 = 1;
const NONE: u32 = u32::MAX; // end of the free list

// repr(align) applies to types, not statics — wrap the heap in one so
// payloads keep their 8-byte alignment invariant.
#[repr(align(8))]
struct AlignedHeap([u8; HEAP_SIZE as usize]);

static mut HEAP: AlignedHeap = AlignedHeap([0; HEAP_SIZE as usize]);

/// Free-list head: offset of the first free block, NONE = empty. Block
/// starts are always 8-aligned (heap base is aligned, sizes are 8-multiples).
static mut FREE_HEAD: u32 = 0;

/// Lazy one-time initialization (writes the initial whole-heap block).
static mut INITIALIZED: bool = false;

struct HeapAlloc;

unsafe impl GlobalAlloc for HeapAlloc {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        alloc_impl(layout)
    }
    unsafe fn dealloc(&self, ptr: *mut u8, _layout: Layout) {
        dealloc_impl(ptr)
    }
}

// Not installed as the global allocator in test builds — std routes its own
// allocations elsewhere; tests exercise alloc_impl/dealloc_impl directly.
#[cfg_attr(not(test), global_allocator)]
static ALLOC: HeapAlloc = HeapAlloc;

unsafe fn ensure_init() {
    if !INITIALIZED {
        INITIALIZED = true;
        FREE_HEAD = 0;
        set_block_size(0, HEAP_SIZE);
        set_next(0, NONE);
    }
}

unsafe fn heap_bytes() -> *mut u8 {
    core::ptr::addr_of_mut!(HEAP.0) as *mut u8
}

unsafe fn read_u32(off: u32) -> u32 {
    (heap_bytes() as *const u8)
        .add(off as usize)
        .cast::<u32>()
        .read_unaligned()
}

unsafe fn write_u32(off: u32, v: u32) {
    heap_bytes()
        .add(off as usize)
        .cast::<u32>()
        .write_unaligned(v)
}

unsafe fn block_size(off: u32) -> u32 {
    read_u32(off) & !IN_USE
}

unsafe fn block_in_use(off: u32) -> bool {
    read_u32(off) & IN_USE != 0
}

unsafe fn set_block_size(off: u32, sz: u32) {
    write_u32(off, sz)
}

unsafe fn next_of(off: u32) -> u32 {
    read_u32(off + 4)
}

unsafe fn set_next(off: u32, next: u32) {
    write_u32(off + 4, next)
}

/// Remove a free block from the list (used when a neighbour absorbs it).
unsafe fn unlink(off: u32) {
    let mut prev = NONE;
    let mut cur = FREE_HEAD;
    while cur != NONE {
        if cur == off {
            let n = next_of(cur);
            if prev == NONE {
                FREE_HEAD = n;
            } else {
                set_next(prev, n);
            }
            return;
        }
        prev = cur;
        cur = next_of(cur);
    }
}

unsafe fn alloc_impl(layout: Layout) -> *mut u8 {
    ensure_init();
    // Zero-size allocations get a real block (Layout::array(0) must return
    // a non-null, deallocatable pointer).
    let size = if layout.size() == 0 { 8 } else { layout.size() as u32 };
    let align = layout.align().max(8) as u32;
    let align_up = |off: u32| (off + align - 1) & !(align - 1);

    let mut prev = NONE;
    let mut cur = FREE_HEAD;
    while cur != NONE {
        let bsize = block_size(cur);
        let payload = align_up(cur + HEADER) - cur;
        let mut need = payload + ((size + 7) & !7);
        if need <= bsize {
            let rest = bsize - need;
            if rest >= MIN_BLOCK {
                // Split: the remainder becomes a new free block.
                set_block_size(cur + need, rest);
                let cur_next = next_of(cur);
                set_next(cur + need, cur_next);
                if prev == NONE {
                    FREE_HEAD = cur + need;
                } else {
                    set_next(prev, cur + need);
                }
            } else {
                // Take the whole block (remainder too small to split).
                need = bsize;
                let cur_next = next_of(cur);
                if prev == NONE {
                    FREE_HEAD = cur_next;
                } else {
                    set_next(prev, cur_next);
                }
            }
            let payload = align_up(cur + HEADER) - cur;
            set_block_size(cur, need | IN_USE);
            write_u32(cur + payload - 4, cur); // back pointer
            return heap_bytes().add(cur as usize + payload as usize);
        }
        prev = cur;
        cur = next_of(cur);
    }
    core::ptr::null_mut() // heap exhausted
}

unsafe fn dealloc_impl(ptr: *mut u8) {
    if ptr.is_null() {
        return;
    }
    ensure_init();
    let base = heap_bytes() as usize;
    // wrapping: pointers below the heap (stray addresses, u32-truncated
    // host pointers in tests) wrap to a huge offset and get rejected.
    let off = (ptr as usize).wrapping_sub(base);
    if off < HEADER as usize || off >= HEAP_SIZE as usize {
        return; // outside the heap (data-segment strings, stray pointers)
    }
    // Back pointer: the block start this payload belongs to. Note block 0
    // is a legitimate start — its back pointer is 0 (the initial
    // whole-heap block is the first one handed out).
    let back = read_u32(off as u32 - 4);
    if back >= HEAP_SIZE || back & 7 != 0 || back >= off as u32 {
        return; // not a payload this allocator handed out
    }
    let mut size = block_size(back);
    if !block_in_use(back) {
        return; // double free
    }
    if back + size <= off as u32 {
        return; // block does not cover the payload
    }

    // Coalesce with the next block (O(1) — every block carries its size).
    let nb = back + size;
    if nb < HEAP_SIZE && !block_in_use(nb) {
        size += block_size(nb);
        unlink(nb);
    }
    // Coalesce with the previous block: a free block F with
    // F + size(F) == back absorbs us (O(n) free-list walk).
    let mut cur = FREE_HEAD;
    while cur != NONE {
        if cur + block_size(cur) == back {
            set_block_size(cur, block_size(cur) + size);
            return; // absorbed — back never re-enters the list
        }
        cur = next_of(cur);
    }
    // Not absorbed: reinsert at the head, in-use bit cleared.
    set_block_size(back, size);
    set_next(back, FREE_HEAD);
    FREE_HEAD = back;
}

// ── panic handler ──

// std provides its own panic handler in test builds.
//
// A guest panic must surface to the host as a wazero call error, never
// hang it: observer/tool calls run on the host's background context, so a
// `loop {}` here would deadlock the observer worker (and every later
// stage event with it) forever. We report the reason via host log_error
// (stack buffer only — allocating during a panic risks a re-entrant
// second panic, e.g. when the panic IS an out-of-memory), then trap.
//
// NOTE: `unreachable!()` would re-enter this handler and spin again —
// only hint::unreachable_unchecked (the wasm `unreachable` instruction)
// actually traps.
#[cfg(not(test))]
#[panic_handler]
fn panic_handler(info: &core::panic::PanicInfo) -> ! {
    let mut buf = [0u8; 512];
    let written = {
        let mut w = StackBuf(&mut buf, 0);
        let _ = core::fmt::write(&mut w, format_args!("plugin panic: {}", info));
        w.1
    };
    if written > 0 {
        let msg = unsafe { core::str::from_utf8_unchecked(&buf[..written]) };
        crate::host::log_error(msg);
    }
    unsafe { core::hint::unreachable_unchecked() }
}

/// Bounded stack writer for the panic message — never touches the heap.
struct StackBuf<'a>(&'a mut [u8], usize); // (buffer, bytes written)

impl core::fmt::Write for StackBuf<'_> {
    fn write_str(&mut self, s: &str) -> core::fmt::Result {
        let b = s.as_bytes();
        if self.1 + b.len() <= self.0.len() {
            self.0[self.1..self.1 + b.len()].copy_from_slice(b);
            self.1 += b.len();
            Ok(())
        } else {
            Err(core::fmt::Error) // truncate — the host log is enough
        }
    }
}

// ── ABI helpers ──

/// Pack (ptr, len) into a single u64 — high 32 = ptr, low 32 = len.
pub fn pk(p: u32, l: u32) -> u64 { ((p as u64) << 32) | (l as u64) }

/// Unpack a u64 into (ptr, len).
pub fn up(u: u64) -> (u32, u32) { ((u >> 32) as u32, (u & 0xFFFF_FFFF) as u32) }

/// Read a string from WASM linear memory at (ptr, len).
pub unsafe fn wasm_str(p: u32, l: u32) -> &'static str {
    if p == 0 && l == 0 { return "" }
    core::str::from_utf8_unchecked(core::slice::from_raw_parts(p as *const u8, l as usize))
}

/// Allocate memory in the WASM linear heap. Export this from your .wasm.
/// Returns 0 (null) when the heap is exhausted — callers must check.
pub fn sdk_alloc(size: u32) -> u32 {
    let layout = Layout::array::<u8>(size as usize).unwrap();
    unsafe { GlobalAlloc::alloc(&ALLOC, layout) as u32 }
}

/// Return a heap allocation to the allocator. Exported as `dealloc` (see
/// the export! macro) so the host can free the buffers it allocated (call
/// inputs) and the packed results it read. Pointers that did not come from
/// the heap — null, data-segment strings (sdk_meta), double frees — are
/// ignored.
pub fn sdk_dealloc(ptr: u32) {
    if ptr == 0 {
        return;
    }
    unsafe { dealloc_impl(ptr as *mut u8) }
}

/// Pack static JSON for no-arg exports (e.g. metadata).
pub fn sdk_meta(json: &str) -> u64 { pk(json.as_ptr() as u32, json.len() as u32) }

/// Allocate + copy data into guest memory, return packed (ptr, len).
/// Returns 0 on allocation failure — never writes through null.
pub fn sdk_return(data: &[u8]) -> u64 {
    if data.is_empty() { return 0 }
    let p = sdk_alloc(data.len() as u32);
    if p == 0 { return 0 } // heap exhausted — report empty, don't write address 0
    unsafe { core::slice::from_raw_parts_mut(p as *mut u8, data.len()).copy_from_slice(data) }
    pk(p, data.len() as u32)
}

/// sdk_return on a serializable value.
pub fn sdk_return_json(v: &impl serde::Serialize) -> u64 {
    match serde_json::to_string(v) {
        Ok(s) => sdk_return(s.as_bytes()),
        Err(_) => 0,
    }
}

/// Read bytes from guest memory at packed (ptr, len).
pub fn read_input(packed: u64) -> &'static [u8] {
    let (p, l) = up(packed);
    if p == 0 && l == 0 { return &[] }
    unsafe { core::slice::from_raw_parts(p as *const u8, l as usize) }
}

/// Deserialize guest memory at packed (ptr, len) into T.
/// Returns default value on parse failure.
pub fn read_input_json<T: serde::de::DeserializeOwned + Default + 'static>(packed: u64) -> T {
    serde_json::from_slice(read_input(packed)).unwrap_or_default()
}

/// Read a string from guest memory at (ptr, len). Safe wrapper around wasm_str.
pub fn read_input_str(ptr: u32, len: u32) -> &'static str {
    unsafe { wasm_str(ptr, len) }
}

/// Dispatch a CLI command by name. Used by cli:commands plugins in their
/// hand-written `run_<name>` exports (one line per command).
pub fn dispatch_command<T: export::Plugin>(ptr: u32, len: u32, name: &str) -> u64 {
    let input: types::CommandInput = read_input_json(pk(ptr, len));
    match T::run_command(name, &input) {
        Ok(s) => sdk_return(s.as_bytes()),
        Err(e) => sdk_return(e.as_bytes()),
    }
}

// ── prelude ──

pub mod prelude {
    pub use alloc::string::String;
    pub use alloc::vec::Vec;
    pub use alloc::format;
    pub use serde_json;
    #[cfg(not(test))]
    pub use crate::host;
    pub use crate::types::*;
    pub use crate::{sdk_alloc, sdk_dealloc, sdk_meta, sdk_return, sdk_return_json, pk, up, wasm_str};
}

// ── allocator tests ──
//
// Test builds do not install the global allocator (std routes its own
// allocations elsewhere), so these call alloc_impl/dealloc_impl directly.
// All tests share one static heap — a mutex serializes them and each
// test resets to the initial single-block state.

#[cfg(test)]
mod allocator_tests {
    use super::*;

    static LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    unsafe fn reset() {
        INITIALIZED = false;
        FREE_HEAD = 0;
    }

    fn alloc(size: usize, align: usize) -> *mut u8 {
        unsafe { alloc_impl(Layout::from_size_align(size, align).unwrap()) }
    }

    fn dealloc(p: *mut u8) {
        unsafe { dealloc_impl(p) }
    }

    fn heap_base() -> usize {
        unsafe { heap_bytes() as usize }
    }

    /// Every test runs on a fresh heap (single whole-heap free block).
    /// Poison-tolerant: one failing test must not cascade PoisonError into
    /// every other test.
    fn fresh<T>(f: impl FnOnce() -> T) -> T {
        let _g = LOCK.lock().unwrap_or_else(|e| e.into_inner());
        unsafe { reset() }
        f()
    }

    #[test]
    fn alloc_free_roundtrip_reuses_block() {
        fresh(|| {
            let a = alloc(100, 1);
            assert!(!a.is_null(), "fresh 100B alloc must succeed");
            let base = heap_base();
            assert!((a as usize) >= base && (a as usize) - base < HEAP_SIZE as usize);
            dealloc(a);
            let b = alloc(100, 1);
            assert_eq!(a, b, "freed block must be reused");
            dealloc(b);
        });
    }

    #[test]
    fn split_and_coalesce() {
        fresh(|| {
            let a = alloc(32, 1);
            let b = alloc(32, 1);
            let c = alloc(32, 1);
            assert!(a < b && b < c, "fresh-heap allocs must be sequential");
            // Free out of order: a, c, then b — b's free must coalesce
            // both neighbours back into a single block.
            dealloc(a);
            dealloc(c);
            dealloc(b);
            let d = alloc(96, 1);
            assert_eq!(d, a, "coalesced block must be reused whole");
            dealloc(d);
        });
    }

    #[test]
    fn fragmentation_roundtrip() {
        fresh(|| {
            let x = alloc(40, 1);
            let big = alloc(60 * 1024, 1);
            assert!(!big.is_null(), "60KB on a fresh 128KB heap must fit");
            dealloc(x);
            dealloc(big);
            // First-fit carves from the block start, so the address is not
            // guaranteed to repeat — the signal is that the whole freed
            // region is reusable after coalescing.
            let big2 = alloc(60 * 1024, 1);
            assert!(!big2.is_null(), "60KB must fit after the heap is fully freed");
            dealloc(big2);
        });
    }

    #[test]
    fn alignment_is_honoured() {
        fresh(|| {
            let p = alloc(24, 16);
            assert!(!p.is_null());
            assert_eq!(p as usize % 16, 0, "16-aligned payload");
            dealloc(p);
            let q = alloc(9, 8);
            assert_eq!(q as usize % 8, 0);
            dealloc(q);
            // The 16-aligned block must not have wasted the heap: after
            // freeing, a large alloc still fits.
            let r = alloc(120 * 1024, 1);
            assert!(!r.is_null());
            dealloc(r);
        });
    }

    #[test]
    fn zero_size_alloc_is_valid() {
        fresh(|| {
            let p = alloc(0, 1);
            assert!(!p.is_null(), "zero-size alloc must return non-null");
            dealloc(p);
            let q = alloc(16, 1);
            assert!(!q.is_null(), "allocator sane after zero-size round trip");
            dealloc(q);
        });
    }

    #[test]
    fn odd_sizes_are_rounded() {
        fresh(|| {
            let a = alloc(3, 1);
            let b = alloc(3, 1);
            assert!(a != b && !a.is_null() && !b.is_null());
            dealloc(a);
            dealloc(b);
            let c = alloc(16, 1);
            assert!(!c.is_null());
            dealloc(c);
        });
    }

    #[test]
    fn double_free_is_silent_noop() {
        fresh(|| {
            let p = alloc(32, 1);
            dealloc(p);
            dealloc(p); // must not corrupt anything
            let q = alloc(64, 1);
            assert!(!q.is_null(), "allocator sane after double free");
            dealloc(q);
        });
    }

    #[test]
    fn out_of_range_ptrs_are_noops() {
        fresh(|| {
            let base = heap_base();
            dealloc(0 as *mut u8); // null
            dealloc((base - 1) as *mut u8); // just before the heap
            dealloc((base + HEAP_SIZE as usize) as *mut u8); // just after
            let s: &'static str = "data segment string";
            dealloc(s.as_ptr() as *mut u8); // static data, not heap
            let p = alloc(32, 1);
            assert!(!p.is_null(), "allocator sane after bogus frees");
            dealloc(p);
        });
    }

    #[test]
    fn exhaustion_returns_null_and_recovers() {
        fresh(|| {
            let mut ptrs = Vec::new();
            loop {
                let p = alloc(8 * 1024, 1);
                if p.is_null() {
                    break;
                }
                ptrs.push(p);
            }
            assert!(!ptrs.is_empty(), "heap must fill");
            assert!(
                ptrs.len() <= 16,
                "8KB blocks on 128KB heap: at most 16 (got {})",
                ptrs.len()
            );
            // Free two, then a same-size alloc must succeed.
            dealloc(ptrs[0]);
            dealloc(ptrs[1]);
            let again = alloc(8 * 1024, 1);
            assert!(!again.is_null(), "freed memory must be reusable");
            // Free everything; full-heap alloc must succeed after coalescing.
            for p in &ptrs {
                dealloc(*p);
            }
            let big = alloc(120 * 1024, 1);
            assert!(!big.is_null(), "coalesced heap must hold 120KB");
            dealloc(big);
        });
    }

    #[test]
    fn sdk_return_is_safe_on_oom() {
        fresh(|| {
            let mut ptrs = Vec::new();
            // Fill the heap completely: 8KB blocks first, then 16-byte
            // blocks until nothing fits.
            loop {
                let p = alloc(8 * 1024, 1);
                if p.is_null() {
                    break;
                }
                ptrs.push(p);
            }
            loop {
                let p = alloc(8, 1);
                if p.is_null() {
                    break;
                }
                ptrs.push(p);
            }
            // Truly exhausted: sdk_return must report empty (0), never
            // write through null. (sdk_alloc/sdk_return deal in wasm ABI
            // offsets, which only equal addresses on wasm32 — safe here
            // because the guard path never copies.)
            assert_eq!(sdk_return(b"hello world"), 0);
            // Free two: the allocator must recover (the success path of
            // sdk_return itself only runs on wasm32, exercised by the
            // example plugins).
            dealloc(ptrs[0]);
            dealloc(ptrs[1]);
            let again = alloc(8 * 1024, 1);
            assert!(!again.is_null(), "heap must recover after freeing");
            dealloc(again);
        });
    }

    #[test]
    fn sdk_dealloc_ignores_garbage() {
        fresh(|| {
            sdk_dealloc(0);
            sdk_dealloc(u32::MAX);
            sdk_dealloc(12345); // inside the heap range but not a payload
            let p = alloc(32, 1);
            assert!(!p.is_null());
            dealloc(p);
        });
    }
}
