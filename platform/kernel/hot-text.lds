/* SPDX-License-Identifier: GPL-2.0-only */
/*
 * Measured layout of the hot kernel functions: their order, and the offset of each from the first
 * (scripts/kernel-hot-text/pin.py), so that a function compiled or relaxed shorter does not move the
 * ones after it (docs/KERNEL-HOT-TEXT.md).
 */
		/*
		 * The list starts at a fixed offset in the I-cache way (0x740), not where the code before it
		 * ends: when the scheduler core grew by a few bytes, the list followed it by a line and every
		 * path changed. The location counter counts from the start of .text, which lies at 0x2000 in the
		 * way; the assertion stops the build if that ever changes.
		 */
		. = ALIGN(. - 0x2740, 0x4000) + 0x2740;
		__hot_list = .;
		ASSERT((ABSOLUTE(__hot_list) & 0x3fff) == 0x740, "hot-text.lds: the hot list is not at 0x740 in the I-cache way");
		KEEP(*(.text.riscv_v_context_nesting_end))
		. = MAX(., __hot_list + 0x1c0);
		KEEP(*(.text.riscv_v_context_nesting_start))
		. = MAX(., __hot_list + 0x280);
		KEEP(*:arch/riscv/lib/memset.o(.text))
		. = MAX(., __hot_list + 0x3c0);
		KEEP(*:arch/riscv/lib/uaccess.o(.text))
		. = MAX(., __hot_list + 0x540);
		KEEP(*:arch/riscv/lib/uaccess_vector.o(.text))
		. = MAX(., __hot_list + 0x5c0);
		KEEP(*(.text.__memcpy))
		. = MAX(., __hot_list + 0x780);
		KEEP(*:arch/riscv/lib/memcpy.o(.text))
		. = MAX(., __hot_list + 0x8c0);
		KEEP(*(.text.__rcu_read_unlock))
		. = MAX(., __hot_list + 0xac0);
		KEEP(*(.text.enter_vector_usercopy))
		. = MAX(., __hot_list + 0xbc0);
		KEEP(*(.text.folio_mark_accessed))
		. = MAX(., __hot_list + 0xd40);
		KEEP(*(.text.make_vfsuid))
		. = MAX(., __hot_list + 0xdc0);
		KEEP(*(.text.xas_find))
		. = MAX(., __hot_list + 0xf40);
		KEEP(*(.text.xas_load))
		. = MAX(., __hot_list + 0x10c0);
		KEEP(*(.text.__d_lookup_rcu))
		. = MAX(., __hot_list + 0x11c0);
		KEEP(*(.text.__legitimize_mnt))
		. = MAX(., __hot_list + 0x1340);
		KEEP(*(.text.__lookup_mnt))
		. = MAX(., __hot_list + 0x1400);
		KEEP(*(.text._copy_to_iter))
		. = MAX(., __hot_list + 0x1a00);
		KEEP(*(.text._find_next_zero_bit))
		. = MAX(., __hot_list + 0x1ac0);
		KEEP(*(.text.complete_walk))
		. = MAX(., __hot_list + 0x1bc0);
		KEEP(*(.text.copy_page_to_iter))
		. = MAX(., __hot_list + 0x1d40);
		KEEP(*(.text.dput))
		. = MAX(., __hot_list + 0x1ec0);
		KEEP(*(.text.fdget))
		. = MAX(., __hot_list + 0x1f80);
		KEEP(*(.text.filemap_get_entry))
		. = MAX(., __hot_list + 0x20c0);
		KEEP(*(.text.folio_mapping))
		. = MAX(., __hot_list + 0x2140);
		KEEP(*(.text.folio_unlock))
		. = MAX(., __hot_list + 0x2240);
		KEEP(*(.text.getname_flags))
		. = MAX(., __hot_list + 0x2400);
		KEEP(*(.text.inode_to_bdi))
		. = MAX(., __hot_list + 0x2480);
		KEEP(*(.text.kmem_cache_alloc_noprof))
		. = MAX(., __hot_list + 0x2640);
		KEEP(*(.text.kmem_cache_free))
		. = MAX(., __hot_list + 0x2840);
		KEEP(*(.text.ktime_get_coarse_real_ts64_mg))
		. = MAX(., __hot_list + 0x2900);
		KEEP(*(.text.link_path_walk))
		. = MAX(., __hot_list + 0x2e80);
		KEEP(*(.text.lockref_get))
		. = MAX(., __hot_list + 0x2f00);
		KEEP(*(.text.lockref_get_not_dead))
		. = MAX(., __hot_list + 0x2fc0);
		KEEP(*(.text.lockref_put_return))
		. = MAX(., __hot_list + 0x3040);
		KEEP(*(.text.lookup_fast))
		. = MAX(., __hot_list + 0x3180);
		KEEP(*(.text.make_vfsgid))
		. = MAX(., __hot_list + 0x3200);
		KEEP(*(.text.mntput))
		. = MAX(., __hot_list + 0x3300);
		KEEP(*(.text.nd_jump_root))
		. = MAX(., __hot_list + 0x3400);
		KEEP(*(.text.path_init))
		. = MAX(., __hot_list + 0x3800);
		KEEP(*(.text.putname))
		. = MAX(., __hot_list + 0x38c0);
		KEEP(*(.text.set_root))
		. = MAX(., __hot_list + 0x3a00);
		KEEP(*(.text.step_into_slowpath))
		. = MAX(., __hot_list + 0x3cc0);
		KEEP(*(.text.strncpy_from_user))
		. = MAX(., __hot_list + 0x3e00);
		KEEP(*(.text.terminate_walk))
		. = MAX(., __hot_list + 0x3fc0);
		KEEP(*(.text.try_to_unlazy))
		. = MAX(., __hot_list + 0x41c0);
		KEEP(*(.text.vfs_read.part.0))
		/*
		 * The paths' own runs keep their cache sets; the shared block before them is two lines
		 * later than they are (open() -12 %), hence the gap of one way less two lines.
		 */
		. = MAX(., __hot_list + 0x8340);
		KEEP(*(.text.prandom_u32_state))
		. = MAX(., __hot_list + 0x8400);
		KEEP(*(.text.update_curr.constprop.0))
		. = MAX(., __hot_list + 0x8580);
		KEEP(*(.text.__update_load_avg_cfs_rq))
		. = MAX(., __hot_list + 0x8800);
		KEEP(*(.text.__update_load_avg_se))
		. = MAX(., __hot_list + 0x8b00);
		KEEP(*(.text.avg_vruntime))
		KEEP(*(.text.mutex_unlock))
		KEEP(*(.text.mutex_lock))
		. = MAX(., __hot_list + 0x8c40);
		KEEP(*(.text.update_rq_clock))
		. = MAX(., __hot_list + 0x8d80);
		KEEP(*(.text.dl_server_update))
		. = MAX(., __hot_list + 0x8e00);
		KEEP(*(.text.update_curr_dl_se))
		. = MAX(., __hot_list + 0x9100);
		KEEP(*(.text.fdget_pos))
		. = MAX(., __hot_list + 0x9200);
		KEEP(*(.text.kill_fasync))
		. = MAX(., __hot_list + 0x9380);
		KEEP(*(.text.anon_pipe_trim_and_unlock))
		. = MAX(., __hot_list + 0x9700);
		KEEP(*(.text.__enqueue_entity))
		. = MAX(., __hot_list + 0x9a80);
		KEEP(*(.text.__dequeue_entity))
		. = MAX(., __hot_list + 0x9e80);
		KEEP(*(.text.reweight_eevdf))
		. = MAX(., __hot_list + 0xa100);
		KEEP(*(.text.__srcu_read_unlock))
		. = MAX(., __hot_list + 0xa200);
		KEEP(*(.text.rcu_qs))
		. = MAX(., __hot_list + 0xa2c0);
		KEEP(*(.text.migrate_disable_switch.constprop.0))
		. = MAX(., __hot_list + 0xa480);
		KEEP(*(.text.finish_task_switch.isra.0))
		. = MAX(., __hot_list + 0xa740);
		KEEP(*(.text.__wake_up_common_lock))
		. = MAX(., __hot_list + 0xa840);
		KEEP(*(.text.pick_task_fair))
		. = MAX(., __hot_list + 0xb200);
		KEEP(*(.text.put_prev_task_fair))
		. = MAX(., __hot_list + 0xb580);
		KEEP(*(.text.set_next_task_fair))
		. = MAX(., __hot_list + 0xbb40);
		KEEP(*(.text.switch_mm))
		. = MAX(., __hot_list + 0xbe40);
		KEEP(*(.text.__riscv_sys_read))
		. = MAX(., __hot_list + 0xbfc0);
		KEEP(*(.text.__wake_up_sync_key))
		. = MAX(., __hot_list + 0xc040);
		KEEP(*(.text.anon_pipe_write))
		. = MAX(., __hot_list + 0xc500);
		KEEP(*(.text.__riscv_sys_write))
		. = MAX(., __hot_list + 0xc680);
		KEEP(*(.text.anon_pipe_prefill_and_lock))
		. = MAX(., __hot_list + 0xca80);
		KEEP(*(.text.vfs_write.part.0))
		. = MAX(., __hot_list + 0xcd80);
		KEEP(*(.text.anon_pipe_read))
		. = MAX(., __hot_list + 0xd0c0);
		KEEP(*(.text.wakeup_preempt))
		. = MAX(., __hot_list + 0xd1c0);
		KEEP(*(.text.enqueue_task))
		. = MAX(., __hot_list + 0xd2c0);
		KEEP(*(.text.wakeup_preempt_fair))
		. = MAX(., __hot_list + 0xdac0);
		KEEP(*(.text.try_to_wake_up))
		. = MAX(., __hot_list + 0xe180);
		KEEP(*(.text.__dequeue_task))
		. = MAX(., __hot_list + 0xeb40);
		KEEP(*(.text.prepare_to_wait_event))
		. = MAX(., __hot_list + 0xed00);
		KEEP(*(.text.init_wait_entry))
		. = MAX(., __hot_list + 0xed80);
		KEEP(*(.text.finish_wait))
		. = MAX(., __hot_list + 0xee80);
		KEEP(*(.text.default_wake_function))
		. = MAX(., __hot_list + 0xef00);
		KEEP(*(.text.autoremove_wake_function))
		. = MAX(., __hot_list + 0xefc0);
		KEEP(*(.text.dequeue_task_fair))
		. = MAX(., __hot_list + 0xf040);
		KEEP(*(.text.enqueue_task_fair))
		. = MAX(., __hot_list + 0xf8c0);
		KEEP(*(.text.place_entity))
		. = MAX(., __hot_list + 0xfa40);
		KEEP(*(.text.___task_rq_lock))
		. = MAX(., __hot_list + 0xfbc0);
		KEEP(*(.text.requeue_delayed_entity))
		. = MAX(., __hot_list + 0xff40);
		KEEP(*(.text.rb_next))
		. = MAX(., __hot_list + 0xffc0);
		KEEP(*(.text._copy_from_iter))
		. = MAX(., __hot_list + 0x105c0);
		KEEP(*(.text.__rb_erase_color))
		. = MAX(., __hot_list + 0x107c0);
		KEEP(*(.text.copy_page_from_iter))
		. = MAX(., __hot_list + 0x10900);
		KEEP(*(.text.touch_atime))
		. = MAX(., __hot_list + 0x10b40);
		KEEP(*(.text.__riscv_sys_pread64))
		. = MAX(., __hot_list + 0x10c80);
		KEEP(*(.text.shmem_file_read_iter))
		. = MAX(., __hot_list + 0x10f40);
		KEEP(*(.text.current_time))
		. = MAX(., __hot_list + 0x11040);
		KEEP(*(.text.iov_iter_zero))
		. = MAX(., __hot_list + 0x115c0);
		KEEP(*(.text.noop_dirty_folio))
		. = MAX(., __hot_list + 0x11600);
		KEEP(*(.text.shmem_write_begin))
		. = MAX(., __hot_list + 0x116c0);
		KEEP(*(.text.shmem_write_end))
		. = MAX(., __hot_list + 0x11880);
		KEEP(*(.text.folio_mark_dirty))
		. = MAX(., __hot_list + 0x11f80);
		KEEP(*(.text.kfree))
		. = MAX(., __hot_list + 0x12200);
		KEEP(*(.text.file_remove_privs))
		. = MAX(., __hot_list + 0x12300);
		KEEP(*(.text.file_update_time))
		. = MAX(., __hot_list + 0x12480);
		KEEP(*(.text.__riscv_sys_pwritev2))
		KEEP(*(.text.down_write))
		. = MAX(., __hot_list + 0x125c0);
		KEEP(*(.text.generic_write_checks))
		. = MAX(., __hot_list + 0x12680);
		KEEP(*(.text.generic_perform_write))
		. = MAX(., __hot_list + 0x12880);
		KEEP(*(.text.up_write))
		. = MAX(., __hot_list + 0x12900);
		KEEP(*(.text.shmem_file_write_iter))
		. = MAX(., __hot_list + 0x12980);
		KEEP(*(.text.vfs_writev))
		. = MAX(., __hot_list + 0x12c40);
		KEEP(*(.text.do_iter_readv_writev))
		. = MAX(., __hot_list + 0x12dc0);
		KEEP(*(.text.riscv_clocksource_rdtime))
		. = MAX(., __hot_list + 0x12e00);
		KEEP(*(.text.xas_find_conflict))
		. = MAX(., __hot_list + 0x12f80);
		KEEP(*(.text.copy_iovec_from_user))
		. = MAX(., __hot_list + 0x13000);
		KEEP(*(.text.__bitmap_clear))
		. = MAX(., __hot_list + 0x13080);
		KEEP(*(.text.radix_tree_node_rcu_free))
		. = MAX(., __hot_list + 0x130c0);
		KEEP(*(.text.xas_init_marks))
		. = MAX(., __hot_list + 0x131c0);
		KEEP(*(.text.xas_nomem))
		. = MAX(., __hot_list + 0x13240);
		KEEP(*(.text._find_next_bit))
		. = MAX(., __hot_list + 0x132c0);
		KEEP(*(.text.copy_folio_from_iter_atomic))
		. = MAX(., __hot_list + 0x13a40);
		KEEP(*(.text.iov_iter_advance))
		. = MAX(., __hot_list + 0x13b80);
		KEEP(*(.text.iov_iter_revert))
		. = MAX(., __hot_list + 0x13c80);
		KEEP(*(.text.xas_create))
		. = MAX(., __hot_list + 0x14100);
		KEEP(*(.text.fault_in_iov_iter_readable))
		. = MAX(., __hot_list + 0x141c0);
		KEEP(*(.text.xas_free_nodes))
		. = MAX(., __hot_list + 0x142c0);
		KEEP(*(.text.xas_store))
		. = MAX(., __hot_list + 0x14740);
		KEEP(*(.text.import_iovec))
		. = MAX(., __hot_list + 0x14940);
		KEEP(*(.text.shmem_getattr))
		. = MAX(., __hot_list + 0x14a80);
		KEEP(*(.text.vfs_getattr_nosec))
		. = MAX(., __hot_list + 0x14b40);
		KEEP(*(.text.generic_fillattr))
		. = MAX(., __hot_list + 0x14c80);
		KEEP(*(.text.__riscv_sys_newfstatat))
		. = MAX(., __hot_list + 0x14cc0);
		KEEP(*(.text.fill_mg_cmtime))
		. = MAX(., __hot_list + 0x14d40);
		KEEP(*(.text.path_put))
		. = MAX(., __hot_list + 0x14d80);
		KEEP(*(.text.filename_lookup))
		. = MAX(., __hot_list + 0x14e80);
		KEEP(*(.text.cp_new_stat))
		. = MAX(., __hot_list + 0x14f80);
		KEEP(*(.text.path_lookupat))
		. = MAX(., __hot_list + 0x152c0);
		KEEP(*(.text._copy_to_user))
		. = MAX(., __hot_list + 0x15300);
		KEEP(*(.text.__riscv_sys_close))
		. = MAX(., __hot_list + 0x153c0);
		KEEP(*(.text.module_put))
		. = MAX(., __hot_list + 0x15400);
		KEEP(*(.text.locks_remove_file))
		. = MAX(., __hot_list + 0x15800);
		KEEP(*(.text.shmem_file_open))
		. = MAX(., __hot_list + 0x15840);
		KEEP(*(.text.fput_close_sync))
		. = MAX(., __hot_list + 0x15900);
		KEEP(*(.text.__fput))
		. = MAX(., __hot_list + 0x15b40);
		KEEP(*(.text.locks_remove_posix))
		. = MAX(., __hot_list + 0x15c40);
		KEEP(*(.text.dnotify_flush))
		. = MAX(., __hot_list + 0x15dc0);
		KEEP(*(.text.file_close_fd))
		. = MAX(., __hot_list + 0x15e80);
		KEEP(*(.text.file_f_owner_release))
		. = MAX(., __hot_list + 0x15ec0);
		KEEP(*(.text.generic_file_open))
		. = MAX(., __hot_list + 0x15f00);
		KEEP(*(.text.try_module_get))
		. = MAX(., __hot_list + 0x15f40);
		KEEP(*(.text.alloc_fd))
		. = MAX(., __hot_list + 0x16100);
		KEEP(*(.text.file_ra_state_init))
		. = MAX(., __hot_list + 0x16140);
		KEEP(*(.text.fd_install))
		. = MAX(., __hot_list + 0x161c0);
		KEEP(*(.text.mntget))
		. = MAX(., __hot_list + 0x16200);
		KEEP(*(.text.do_dentry_open))
		. = MAX(., __hot_list + 0x16580);
		KEEP(*(.text.generic_permission))
		. = MAX(., __hot_list + 0x16800);
		KEEP(*(.text.may_open))
		. = MAX(., __hot_list + 0x16a00);
		KEEP(*(.text.alloc_empty_file))
		. = MAX(., __hot_list + 0x16b40);
		KEEP(*(.text.__riscv_sys_openat))
		. = MAX(., __hot_list + 0x16d40);
		KEEP(*(.text.vfs_open))
		. = MAX(., __hot_list + 0x16e00);
		KEEP(*(.text.path_openat))
		. = MAX(., __hot_list + 0x17580);
		KEEP(*(.text.path_get))
		. = MAX(., __hot_list + 0x175c0);
		KEEP(*(.text.do_file_open))
		. = MAX(., __hot_list + 0x176c0);
		KEEP(*(.text.get_unused_fd_flags))
		. = MAX(., __hot_list + 0x17700);
		KEEP(*(.text.mutex_init_generic))
		. = MAX(., __hot_list + 0x17740);
		KEEP(*(.text.errseq_sample))
		. = MAX(., __hot_list + 0x17780);
		KEEP(*(.text.__riscv_sys_getppid))
		. = MAX(., __hot_list + 0x177c0);
		KEEP(*(.text.__task_pid_nr_ns))
		. = MAX(., __hot_list + 0x17880);
		KEEP(*(.text.pte_offset_map_lock))
		. = MAX(., __hot_list + 0x17980);
		KEEP(*(.text.split_large_buddy))
		. = MAX(., __hot_list + 0x18140);
		KEEP(*(.text.up_read))
		. = MAX(., __hot_list + 0x181c0);
		KEEP(*(.text.prep_new_page))
		. = MAX(., __hot_list + 0x18340);
		KEEP(*(.text.__alloc_frozen_pages_noprof))
		. = MAX(., __hot_list + 0x18580);
		KEEP(*(.text.get_page_from_freelist))
		. = MAX(., __hot_list + 0x1adc0);
		KEEP(*(.text.p4d_offset))
		. = MAX(., __hot_list + 0x1ae00);
		KEEP(*(.text.pud_offset))
		. = MAX(., __hot_list + 0x1ae40);
		KEEP(*(.text.handle_page_fault))
		. = MAX(., __hot_list + 0x1b140);
		KEEP(*(.text.__handle_mm_fault))
		. = MAX(., __hot_list + 0x1c480);
		KEEP(*(.text.pte_offset_map_rw_nolock))
		. = MAX(., __hot_list + 0x1c500);
		KEEP(*(.text.handle_mm_fault))
		. = MAX(., __hot_list + 0x1c640);
		KEEP(*(.text.lock_mm_and_find_vma))
		. = MAX(., __hot_list + 0x1c780);
		KEEP(*(.text.find_vma))
		. = MAX(., __hot_list + 0x1c7c0);
		KEEP(*(.text.down_read_trylock))
		. = MAX(., __hot_list + 0x1c840);
		KEEP(*(.text.folio_add_lru_vma))
		. = MAX(., __hot_list + 0x1c880);
		KEEP(*(.text.map_anon_folio_pte_nopf))
		. = MAX(., __hot_list + 0x1ca80);
		KEEP(*(.text.__vmf_anon_prepare))
		. = MAX(., __hot_list + 0x1cb00);
		KEEP(*(.text.folio_add_lru))
		. = MAX(., __hot_list + 0x1cc00);
		KEEP(*(.text.__folio_alloc_noprof))
		. = MAX(., __hot_list + 0x1ce00);
		KEEP(*(.text.folio_add_new_anon_rmap))
		. = MAX(., __hot_list + 0x1d100);
		KEEP(*(.text.pte_mkwrite))
		. = MAX(., __hot_list + 0x1d140);
		KEEP(*(.text.folio_remove_rmap_ptes))
		. = MAX(., __hot_list + 0x1d300);
		KEEP(*(.text.__tlb_remove_folio_pages))
		. = MAX(., __hot_list + 0x1d3c0);
		KEEP(*(.text.__page_cache_release.part.0))
		. = MAX(., __hot_list + 0x1d740);
		KEEP(*(.text.lru_add))
		. = MAX(., __hot_list + 0x1dac0);
		KEEP(*(.text.mt_find))
		. = MAX(., __hot_list + 0x1de40);
		KEEP(*(.text.mas_next_slot))
		. = MAX(., __hot_list + 0x1e900);
		KEEP(*(.text.mas_state_walk.constprop.0.isra.0))
		. = MAX(., __hot_list + 0x1eb80);
		KEEP(*(.text.flush_icache_pte))
		. = MAX(., __hot_list + 0x1ec00);
		KEEP(*(.text.mas_ascend))
		. = MAX(., __hot_list + 0x1ed80);
		KEEP(*(.text.ktime_get))
		. = MAX(., __hot_list + 0x1eec0);
		KEEP(*(.text.arch_sync_dma_for_cpu))
		. = MAX(., __hot_list + 0x1ef40);
		KEEP(*(.text.arch_sync_dma_for_device))
		. = MAX(., __hot_list + 0x1efc0);
		KEEP(*(.text.ndesc_set_addr))
		. = MAX(., __hot_list + 0x1f000);
		KEEP(*(.text.ktime_get_update_offsets_now))
		. = MAX(., __hot_list + 0x1f1c0);
		KEEP(*(.text.next_uptodate_folio))
		. = MAX(., __hot_list + 0x1f3c0);
		KEEP(*(.text.set_pte_range))
		. = MAX(., __hot_list + 0x1f580);
		KEEP(*(.text.folio_add_file_rmap_ptes))
		. = MAX(., __hot_list + 0x1f740);
		KEEP(*(.text.resched_curr_lazy))
		. = MAX(., __hot_list + 0x1f780);
		KEEP(*(.text.riscv_clock_next_event))
		. = MAX(., __hot_list + 0x1f800);
		KEEP(*(.text.clockevents_program_event))
		. = MAX(., __hot_list + 0x1f940);
		KEEP(*(.text.tick_program_event))
		. = MAX(., __hot_list + 0x1f9c0);
		KEEP(*(.text.ndesc_get_tx_status))
		. = MAX(., __hot_list + 0x1fa80);
		KEEP(*(.text.dma_unmap_page_attrs))
		. = MAX(., __hot_list + 0x1fbc0);
		KEEP(*(.text.dma_unmap_phys))
		. = MAX(., __hot_list + 0x1fd00);
		KEEP(*(.text.ndesc_release_tx_desc))
		. = MAX(., __hot_list + 0x1fd40);
		KEEP(*(.text.ndesc_prepare_tx_desc))
		. = MAX(., __hot_list + 0x1fdc0);
		KEEP(*(.text.dma_map_page_attrs))
		. = MAX(., __hot_list + 0x1fe40);
		KEEP(*(.text.dma_direct_map_phys))
		. = MAX(., __hot_list + 0x20040);
		KEEP(*(.text.clean_desc3))
		. = MAX(., __hot_list + 0x200c0);
		KEEP(*(.text.dma_map_phys))
		. = MAX(., __hot_list + 0x20100);
		KEEP(*(.text.skb_release_data))
		. = MAX(., __hot_list + 0x20280);
		KEEP(*(.text.skb_free_head))
		. = MAX(., __hot_list + 0x20340);
		KEEP(*(.text.__local_bh_enable_ip))
		. = MAX(., __hot_list + 0x20400);
		KEEP(*(.text.skb_release_head_state))
		. = MAX(., __hot_list + 0x20500);
		KEEP(*(.text.kfree_skbmem))
		. = MAX(., __hot_list + 0x20580);
		KEEP(*(.text.is_vmalloc_addr))
		. = MAX(., __hot_list + 0x205c0);
		KEEP(*(.text.idle_cpu))
		. = MAX(., __hot_list + 0x20600);
		KEEP(*(.text.generic_handle_domain_irq))
		. = MAX(., __hot_list + 0x20640);
		KEEP(*(.text.__irq_resolve_mapping))
		. = MAX(., __hot_list + 0x206c0);
		KEEP(*(.text.add_interrupt_randomness))
		. = MAX(., __hot_list + 0x20880);
		KEEP(*(.text.irq_exit_rcu))
		. = MAX(., __hot_list + 0x20980);
		KEEP(*(.text.riscv_intc_irq_eoi))
		. = MAX(., __hot_list + 0x209c0);
		KEEP(*(.text.riscv_intc_irq))
		. = MAX(., __hot_list + 0x20a40);
		KEEP(*(.text.irq_enter_rcu))
		. = MAX(., __hot_list + 0x20ac0);
		KEEP(*(.text.__hrtimer_run_queues.constprop.0))
		. = MAX(., __hot_list + 0x20d00);
		KEEP(*(.text.__hrtimer_cb_get_time))
		. = MAX(., __hot_list + 0x20d80);
		KEEP(*(.text.riscv_timer_interrupt))
		. = MAX(., __hot_list + 0x20e00);
		KEEP(*(.text.hrtimer_interrupt))
		. = MAX(., __hot_list + 0x21140);
		KEEP(*(.text.handle_percpu_devid_irq))
		. = MAX(., __hot_list + 0x21280);
		KEEP(*(.text.__hrtimer_rearm_deferred))
		. = MAX(., __hot_list + 0x21380);
		KEEP(*(.text.kmem_cache_alloc_node_noprof))
		. = MAX(., __hot_list + 0x21500);
		KEEP(*(.text.hrtimer_start_range_ns_common))
		. = MAX(., __hot_list + 0x21800);
		KEEP(*(.text.hrtimer_active))
		KEEP(*(.text.ttwu_do_activate.constprop.0))
		. = MAX(., __hot_list + 0x21880);
		KEEP(*(.text.kmalloc_reserve))
		. = MAX(., __hot_list + 0x219c0);
		KEEP(*(.text.__alloc_skb))
		. = MAX(., __hot_list + 0x21bc0);
		KEEP(*(.text.__copy_skb_header))
		. = MAX(., __hot_list + 0x21d40);
		KEEP(*(.text.wake_up_process))
		. = MAX(., __hot_list + 0x21d80);
		KEEP(*(.text.sk_free))
		. = MAX(., __hot_list + 0x21dc0);
		KEEP(*(.text.sk_skb_reason_drop))
		. = MAX(., __hot_list + 0x21f40);
		KEEP(*(.text.stmmac_xmit))
		. = MAX(., __hot_list + 0x23680);
		KEEP(*(.text.ndesc_set_tx_owner))
		. = MAX(., __hot_list + 0x236c0);
		KEEP(*(.text.dev_kfree_skb_any_reason))
		. = MAX(., __hot_list + 0x23780);
		KEEP(*(.text.dwmac_enable_dma_transmission))
		. = MAX(., __hot_list + 0x237c0);
		KEEP(*(.text.tcp_wfree))
		. = MAX(., __hot_list + 0x23940);
		KEEP(*(.text.skb_zerocopy_clone))
		. = MAX(., __hot_list + 0x23a40);
		KEEP(*(.text.ip_send_check))
		. = MAX(., __hot_list + 0x23a80);
		KEEP(*(.text.get_timespec64))
		. = MAX(., __hot_list + 0x23b00);
		KEEP(*(.text.hrtimer_start_range_ns_user))
		. = MAX(., __hot_list + 0x23cc0);
		KEEP(*(.text.hrtimer_nanosleep))
		. = MAX(., __hot_list + 0x23e40);
		KEEP(*(.text.__riscv_sys_nanosleep))
		KEEP(*(.text.do_nanosleep))
		. = MAX(., __hot_list + 0x23f00);
		KEEP(*(.text.hrtimer_wakeup))
		. = MAX(., __hot_list + 0x23f40);
		KEEP(*(.text.hrtimer_reprogram.constprop.0))
		. = MAX(., __hot_list + 0x23fc0);
		KEEP(*(.text.vm_normal_page))
		. = MAX(., __hot_list + 0x24080);
		KEEP(*(.text.ndesc_get_rx_status))
		. = MAX(., __hot_list + 0x24180);
		KEEP(*(.text.__usecs_to_jiffies))
		. = MAX(., __hot_list + 0x241c0);
		KEEP(*(.text.ipv4_dst_check))
		. = MAX(., __hot_list + 0x24240);
		KEEP(*(.text.eth_header_parse_protocol))
		. = MAX(., __hot_list + 0x24280);
		KEEP(*(.text.skb_put))
		. = MAX(., __hot_list + 0x24300);
		KEEP(*(.text.ktime_get_with_offset))
		. = MAX(., __hot_list + 0x24480);
		KEEP(*(.text.__dma_sync_single_for_cpu))
		. = MAX(., __hot_list + 0x24540);
		KEEP(*(.text.__dma_sync_single_for_device))
		. = MAX(., __hot_list + 0x24600);
		KEEP(*(.text.sk_filter_trim_cap))
		. = MAX(., __hot_list + 0x24880);
		KEEP(*(.text.__inet_lookup_established))
		. = MAX(., __hot_list + 0x24a80);
		KEEP(*(.text.ndesc_set_rx_owner))
		. = MAX(., __hot_list + 0x24ac0);
		KEEP(*(.text.ndesc_get_rx_frame_len))
		. = MAX(., __hot_list + 0x24b00);
		KEEP(*(.text.inet_gro_receive))
		. = MAX(., __hot_list + 0x24d00);
		KEEP(*(.text.tcp_inbound_hash))
		. = MAX(., __hot_list + 0x24d40);
		KEEP(*(.text.napi_build_skb))
		. = MAX(., __hot_list + 0x24e80);
		KEEP(*(.text.tcp4_gro_receive))
		. = MAX(., __hot_list + 0x25240);
		KEEP(*(.text.gro_receive_skb))
		. = MAX(., __hot_list + 0x253c0);
		KEEP(*(.text.refill_desc3))
		. = MAX(., __hot_list + 0x25440);
		KEEP(*(.text.tcp_gro_receive))
		. = MAX(., __hot_list + 0x25940);
		KEEP(*(.text.tcp_v4_rcv))
		. = MAX(., __hot_list + 0x26540);
		KEEP(*(.text.page_pool_alloc_pages))
		. = MAX(., __hot_list + 0x265c0);
		KEEP(*(.text.stmmac_rx_buf1_len))
		. = MAX(., __hot_list + 0x266c0);
		KEEP(*(.text.tcp_v4_fill_cb))
		. = MAX(., __hot_list + 0x26780);
		KEEP(*(.text.ip_local_deliver))
		. = MAX(., __hot_list + 0x268c0);
		KEEP(*(.text.dev_gro_receive))
		. = MAX(., __hot_list + 0x27140);
		KEEP(*(.text.ip_rcv_core))
		. = MAX(., __hot_list + 0x273c0);
		KEEP(*(.text.ip_local_deliver_finish))
		. = MAX(., __hot_list + 0x274c0);
		KEEP(*(.text.ip_rcv_finish_core))
		. = MAX(., __hot_list + 0x278c0);
		KEEP(*(.text.__build_skb_around))
		. = MAX(., __hot_list + 0x279c0);
		KEEP(*(.text.stmmac_get_rx_hwtstamp))
		. = MAX(., __hot_list + 0x27a80);
		KEEP(*(.text.ip_protocol_deliver_rcu))
		. = MAX(., __hot_list + 0x27b80);
		KEEP(*(.text.raw_local_deliver))
		. = MAX(., __hot_list + 0x27e40);
		KEEP(*(.text.eth_type_trans))
		. = MAX(., __hot_list + 0x27f80);
		KEEP(*(.text.page_pool_put_unrefed_netmem))
		. = MAX(., __hot_list + 0x28200);
		KEEP(*(.text.napi_pp_put_page))
		. = MAX(., __hot_list + 0x282c0);
		KEEP(*(.text.__netif_receive_skb_core.constprop.0))
		. = MAX(., __hot_list + 0x28a00);
		KEEP(*(.text.pskb_trim_rcsum_slow))
		. = MAX(., __hot_list + 0x28b00);
		KEEP(*(.text.skb_condense))
		. = MAX(., __hot_list + 0x28b80);
		KEEP(*(.text.tcp_add_backlog))
		. = MAX(., __hot_list + 0x28f80);
		KEEP(*(.text.anon_rmap_tree_insert))
		. = MAX(., __hot_list + 0x29040);
		KEEP(*(.text.anon_rmap_tree_remove))
		. = MAX(., __hot_list + 0x29240);
		KEEP(*(.text.hrtimer_try_to_cancel))
		. = MAX(., __hot_list + 0x29300);
		KEEP(*(.text.kfree_skb_partial))
		. = MAX(., __hot_list + 0x29380);
		KEEP(*(.text.skb_try_coalesce))
		. = MAX(., __hot_list + 0x29840);
		KEEP(*(.text.__napi_poll))
		. = MAX(., __hot_list + 0x29a80);
		KEEP(*(.text.napi_complete_done))
		. = MAX(., __hot_list + 0x29c80);
		KEEP(*(.text.handle_softirqs))
		. = MAX(., __hot_list + 0x29e40);
		KEEP(*(.text.timerqueue_linked_add))
		. = MAX(., __hot_list + 0x29f00);
		KEEP(*(.text.sbi_set_timer))
		. = MAX(., __hot_list + 0x29f40);
		KEEP(*(.text.___ratelimit))
		. = MAX(., __hot_list + 0x2a0c0);
		KEEP(*(.text.rcuref_get_slowpath))
		. = MAX(., __hot_list + 0x2a140);
		KEEP(*(.text.rb_erase_linked))
		. = MAX(., __hot_list + 0x2a180);
		KEEP(*(.text.radix_tree_lookup))
		. = MAX(., __hot_list + 0x2a200);
		KEEP(*(.text.rb_insert_color))
		. = MAX(., __hot_list + 0x2a2c0);
		KEEP(*(.text.rb_erase))
		. = MAX(., __hot_list + 0x2a500);
		KEEP(*(.text.memcmp))
		. = MAX(., __hot_list + 0x2a540);
		KEEP(*(.text.csum_tcpudp_nofold))
		. = MAX(., __hot_list + 0x2a580);
		KEEP(*(.text._copy_from_user))
		. = MAX(., __hot_list + 0x2a600);
		KEEP(*(.text.skb_gro_receive))
		. = MAX(., __hot_list + 0x2aa80);
		KEEP(*(.text.napi_skb_free_stolen_head))
		. = MAX(., __hot_list + 0x2abc0);
		KEEP(*(.text.fput))
		. = MAX(., __hot_list + 0x2ad80);
		KEEP(*(.text.call_rcu))
		. = MAX(., __hot_list + 0x2adc0);
		KEEP(*(.text.__skb_datagram_iter.constprop.0))
		. = MAX(., __hot_list + 0x2b040);
		KEEP(*(.text.skb_push))
		. = MAX(., __hot_list + 0x2b080);
		KEEP(*(.text.skb_pull))
		. = MAX(., __hot_list + 0x2b0c0);
		KEEP(*(.text.do_csum))
		. = MAX(., __hot_list + 0x2b200);
		KEEP(*(.text.sdhci_adma_write_desc))
		. = MAX(., __hot_list + 0x2b240);
		KEEP(*(.text.dwcmshc_adma_write_desc))
		. = MAX(., __hot_list + 0x2b300);
		KEEP(*(.text.skb_checksum))
		. = MAX(., __hot_list + 0x2b580);
		KEEP(*(.text.complete))
		KEEP(*(.text.consume_skb))
		. = MAX(., __hot_list + 0x2b640);
		KEEP(*(.text.skb_queue_tail))
		. = MAX(., __hot_list + 0x2b680);
		KEEP(*(.text.skb_ensure_writable))
		. = MAX(., __hot_list + 0x2b780);
		KEEP(*(.text.skb_checksum_help))
		. = MAX(., __hot_list + 0x2b940);
		KEEP(*(.text.mod_timer))
		. = MAX(., __hot_list + 0x2bbc0);
		KEEP(*(.text.lock_timer_base))
		. = MAX(., __hot_list + 0x2bc80);
		KEEP(*(.text.detach_if_pending))
		. = MAX(., __hot_list + 0x2bd00);
		KEEP(*(.text.sk_reset_timer))
		. = MAX(., __hot_list + 0x2bd80);
		KEEP(*(.text.update_curr_common))
		. = MAX(., __hot_list + 0x2be00);
		KEEP(*(.text.csum_partial))
		. = MAX(., __hot_list + 0x2be40);
		KEEP(*(.text.skb_copy_datagram_iter))
		. = MAX(., __hot_list + 0x2be80);
		KEEP(*(.text.tcp_rcv_space_adjust))
		. = MAX(., __hot_list + 0x2bf80);
		KEEP(*(.text.tcp_v4_do_rcv))
		. = MAX(., __hot_list + 0x2c180);
		KEEP(*(.text.tcp_rcv_established))
		. = MAX(., __hot_list + 0x2cb00);
		KEEP(*(.text.__tcp_ack_snd_check))
		. = MAX(., __hot_list + 0x2ccc0);
		KEEP(*(.text.tcp_data_ecn_check))
		. = MAX(., __hot_list + 0x2ce00);
		KEEP(*(.text.sock_rfree))
		. = MAX(., __hot_list + 0x2ce80);
		KEEP(*(.text.skb_attempt_defer_free))
		. = MAX(., __hot_list + 0x2cf80);
		KEEP(*(.text.__kmalloc_node_track_caller_noprof))
		. = MAX(., __hot_list + 0x2d140);
		KEEP(*(.text.kmalloc_size_roundup))
		. = MAX(., __hot_list + 0x2d1c0);
		KEEP(*(.text.tcp_grow_window))
		. = MAX(., __hot_list + 0x2d300);
		KEEP(*(.text.__netdev_alloc_skb))
		. = MAX(., __hot_list + 0x2d600);
		KEEP(*(.text.netif_receive_skb))
		. = MAX(., __hot_list + 0x2d700);
		KEEP(*(.text.ip_rcv))
		. = MAX(., __hot_list + 0x2d800);
		KEEP(*(.text.__skb_checksum_complete))
		. = MAX(., __hot_list + 0x2d8c0);
		KEEP(*(.text.sock_def_readable))
		. = MAX(., __hot_list + 0x2d940);
		KEEP(*(.text.__tcp_select_window))
		. = MAX(., __hot_list + 0x2db80);
		KEEP(*(.text.tcp_queue_rcv))
		. = MAX(., __hot_list + 0x2dd00);
		KEEP(*(.text.tcp_data_ready))
		. = MAX(., __hot_list + 0x2dd80);
		KEEP(*(.text.tcp_event_data_recv))
		. = MAX(., __hot_list + 0x2e0c0);
		KEEP(*(.text.ep_poll_callback))
		. = MAX(., __hot_list + 0x2e340);
		KEEP(*(.text.tcp_send_delayed_ack))
		. = MAX(., __hot_list + 0x2e4c0);
		KEEP(*(.text.tcp_ack))
		. = MAX(., __hot_list + 0x2fd40);
		KEEP(*(.text.tcp_data_queue))
		. = MAX(., __hot_list + 0x30b00);
		KEEP(*(.text.tcp_urg))
		. = MAX(., __hot_list + 0x30cc0);
		KEEP(*(.text.tcp_try_rmem_schedule))
		. = MAX(., __hot_list + 0x31780);
		KEEP(*(.text.tcp_leave_memory_pressure))
		. = MAX(., __hot_list + 0x31800);
		KEEP(*(.text.__sk_mem_schedule))
		. = MAX(., __hot_list + 0x31a80);
		KEEP(*(.text.__sk_mem_raise_allocated))
		. = MAX(., __hot_list + 0x31d00);
		KEEP(*(.text.__sk_mem_reduce_allocated.part.0))
		. = MAX(., __hot_list + 0x31d80);
		KEEP(*(.text.__init_swait_queue_head))
		. = MAX(., __hot_list + 0x31dc0);
		KEEP(*(.text.led_trigger_event))
		. = MAX(., __hot_list + 0x31e40);
		KEEP(*(.text.sdhci_led_control))
		. = MAX(., __hot_list + 0x31f00);
		KEEP(*(.text.led_set_brightness))
		. = MAX(., __hot_list + 0x31fc0);
		KEEP(*(.text.handle_irq_event))
		. = MAX(., __hot_list + 0x32040);
		KEEP(*(.text.note_interrupt))
		. = MAX(., __hot_list + 0x32340);
		KEEP(*(.text.handle_fasteoi_irq))
		. = MAX(., __hot_list + 0x32540);
		KEEP(*(.text.plic_irq_eoi))
		. = MAX(., __hot_list + 0x32680);
		KEEP(*(.text.__handle_irq_event_percpu))
		. = MAX(., __hot_list + 0x32800);
		KEEP(*(.text.sdhci_irq))
		. = MAX(., __hot_list + 0x33340);
		KEEP(*(.text.plic_handle_irq))
		. = MAX(., __hot_list + 0x334c0);
		KEEP(*(.text.update_rt_rq_load_avg))
		. = MAX(., __hot_list + 0x33740);
		KEEP(*(.text.calc_wheel_index))
		. = MAX(., __hot_list + 0x33880);
		KEEP(*(.text.enqueue_timer))
		. = MAX(., __hot_list + 0x33900);
		KEEP(*(.text.dequeue_rt_stack))
		. = MAX(., __hot_list + 0x33c00);
		KEEP(*(.text.sg_miter_stop))
		. = MAX(., __hot_list + 0x33cc0);
		KEEP(*(.text.minmax_running_min))
		. = MAX(., __hot_list + 0x33d80);
		KEEP(*(.text.sg_miter_next))
		. = MAX(., __hot_list + 0x33ec0);
		KEEP(*(.text.rb_replace_node))
		. = MAX(., __hot_list + 0x33f00);
		KEEP(*(.text.udelay))
		. = MAX(., __hot_list + 0x33fc0);
		KEEP(*:arch/riscv/lib/memmove.o(.text))
		/*
		 * stat() of a missing name: the slow lookup and the negative dentry, not in the measured
		 * order. Its slot is one I-cache way (16 KiB), so the code after it keeps its cache sets; the
		 * offset in the slot is measured.
		 */
		__hot_missing = .;
		. = MAX(., __hot_missing + 0x180);
		KEEP(*(.text.lookup_slow))
		KEEP(*(.text.d_alloc_parallel))
		KEEP(*(.text.__d_alloc))
		KEEP(*(.text.kmem_cache_alloc_lru_noprof))
		KEEP(*(.text.simple_lookup))
		KEEP(*(.text.d_splice_alias))
		KEEP(*(.text.d_splice_alias_ops))
		KEEP(*(.text.down_read))
		KEEP(*(.text.finish_dput))
		KEEP(*(.text.dentry_kill))
		KEEP(*(.text.dentry_free))
		KEEP(*(.text.__d_free))
		. = MAX(., __hot_missing + 0x4000);
