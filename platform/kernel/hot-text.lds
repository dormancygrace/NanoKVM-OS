/* SPDX-License-Identifier: GPL-2.0-only */
/*
 * Measured layout of the hot kernel functions: their order, and the offset of each from the first
 * (scripts/kernel-hot-text/pin.py), so that a function compiled or relaxed shorter does not move the
 * ones after it (docs/KERNEL-HOT-TEXT.md).
 */
		. = ALIGN(. + 62, 64);
		__hot_list = .;
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
		. = MAX(., __hot_list + 0x43c0);
		KEEP(*(.text.prandom_u32_state))
		. = MAX(., __hot_list + 0x4480);
		KEEP(*(.text.update_curr.constprop.0))
		. = MAX(., __hot_list + 0x4600);
		KEEP(*(.text.__update_load_avg_cfs_rq))
		. = MAX(., __hot_list + 0x4880);
		KEEP(*(.text.__update_load_avg_se))
		. = MAX(., __hot_list + 0x4b80);
		KEEP(*(.text.avg_vruntime))
		KEEP(*(.text.mutex_unlock))
		KEEP(*(.text.mutex_lock))
		. = MAX(., __hot_list + 0x4cc0);
		KEEP(*(.text.update_rq_clock))
		. = MAX(., __hot_list + 0x4e00);
		KEEP(*(.text.dl_server_update))
		. = MAX(., __hot_list + 0x4e80);
		KEEP(*(.text.update_curr_dl_se))
		. = MAX(., __hot_list + 0x5180);
		KEEP(*(.text.fdget_pos))
		. = MAX(., __hot_list + 0x5280);
		KEEP(*(.text.kill_fasync))
		. = MAX(., __hot_list + 0x5400);
		KEEP(*(.text.anon_pipe_trim_and_unlock))
		. = MAX(., __hot_list + 0x5780);
		KEEP(*(.text.__enqueue_entity))
		. = MAX(., __hot_list + 0x5b00);
		KEEP(*(.text.__dequeue_entity))
		. = MAX(., __hot_list + 0x5f00);
		KEEP(*(.text.reweight_eevdf))
		. = MAX(., __hot_list + 0x6180);
		KEEP(*(.text.__srcu_read_unlock))
		. = MAX(., __hot_list + 0x6280);
		KEEP(*(.text.rcu_qs))
		. = MAX(., __hot_list + 0x6340);
		KEEP(*(.text.migrate_disable_switch.constprop.0))
		. = MAX(., __hot_list + 0x6500);
		KEEP(*(.text.finish_task_switch.isra.0))
		. = MAX(., __hot_list + 0x67c0);
		KEEP(*(.text.__wake_up_common_lock))
		. = MAX(., __hot_list + 0x68c0);
		KEEP(*(.text.pick_task_fair))
		. = MAX(., __hot_list + 0x7280);
		KEEP(*(.text.put_prev_task_fair))
		. = MAX(., __hot_list + 0x7600);
		KEEP(*(.text.set_next_task_fair))
		. = MAX(., __hot_list + 0x7bc0);
		KEEP(*(.text.switch_mm))
		. = MAX(., __hot_list + 0x7ec0);
		KEEP(*(.text.__riscv_sys_read))
		. = MAX(., __hot_list + 0x8040);
		KEEP(*(.text.__wake_up_sync_key))
		. = MAX(., __hot_list + 0x80c0);
		KEEP(*(.text.anon_pipe_write))
		. = MAX(., __hot_list + 0x8580);
		KEEP(*(.text.__riscv_sys_write))
		. = MAX(., __hot_list + 0x8700);
		KEEP(*(.text.anon_pipe_prefill_and_lock))
		. = MAX(., __hot_list + 0x8b00);
		KEEP(*(.text.vfs_write.part.0))
		. = MAX(., __hot_list + 0x8e00);
		KEEP(*(.text.anon_pipe_read))
		. = MAX(., __hot_list + 0x9140);
		KEEP(*(.text.wakeup_preempt))
		. = MAX(., __hot_list + 0x9240);
		KEEP(*(.text.enqueue_task))
		. = MAX(., __hot_list + 0x9340);
		KEEP(*(.text.wakeup_preempt_fair))
		. = MAX(., __hot_list + 0x9b40);
		KEEP(*(.text.try_to_wake_up))
		. = MAX(., __hot_list + 0xa200);
		KEEP(*(.text.__dequeue_task))
		. = MAX(., __hot_list + 0xabc0);
		KEEP(*(.text.prepare_to_wait_event))
		. = MAX(., __hot_list + 0xad80);
		KEEP(*(.text.init_wait_entry))
		. = MAX(., __hot_list + 0xae00);
		KEEP(*(.text.finish_wait))
		. = MAX(., __hot_list + 0xaf00);
		KEEP(*(.text.default_wake_function))
		. = MAX(., __hot_list + 0xaf80);
		KEEP(*(.text.autoremove_wake_function))
		. = MAX(., __hot_list + 0xb040);
		KEEP(*(.text.dequeue_task_fair))
		. = MAX(., __hot_list + 0xb0c0);
		KEEP(*(.text.enqueue_task_fair))
		. = MAX(., __hot_list + 0xb940);
		KEEP(*(.text.place_entity))
		. = MAX(., __hot_list + 0xbac0);
		KEEP(*(.text.___task_rq_lock))
		. = MAX(., __hot_list + 0xbc40);
		KEEP(*(.text.requeue_delayed_entity))
		. = MAX(., __hot_list + 0xbfc0);
		KEEP(*(.text.rb_next))
		. = MAX(., __hot_list + 0xc040);
		KEEP(*(.text._copy_from_iter))
		. = MAX(., __hot_list + 0xc640);
		KEEP(*(.text.__rb_erase_color))
		. = MAX(., __hot_list + 0xc840);
		KEEP(*(.text.copy_page_from_iter))
		. = MAX(., __hot_list + 0xc980);
		KEEP(*(.text.touch_atime))
		. = MAX(., __hot_list + 0xcbc0);
		KEEP(*(.text.__riscv_sys_pread64))
		. = MAX(., __hot_list + 0xcd00);
		KEEP(*(.text.shmem_file_read_iter))
		. = MAX(., __hot_list + 0xcfc0);
		KEEP(*(.text.current_time))
		. = MAX(., __hot_list + 0xd0c0);
		KEEP(*(.text.iov_iter_zero))
		. = MAX(., __hot_list + 0xd640);
		KEEP(*(.text.noop_dirty_folio))
		. = MAX(., __hot_list + 0xd680);
		KEEP(*(.text.shmem_write_begin))
		. = MAX(., __hot_list + 0xd700);
		KEEP(*(.text.shmem_write_end))
		. = MAX(., __hot_list + 0xd8c0);
		KEEP(*(.text.folio_mark_dirty))
		. = MAX(., __hot_list + 0xd940);
		KEEP(*(.text.shmem_get_folio_gfp.isra.0))
		. = MAX(., __hot_list + 0xe000);
		KEEP(*(.text.kfree))
		. = MAX(., __hot_list + 0xe280);
		KEEP(*(.text.file_remove_privs))
		. = MAX(., __hot_list + 0xe380);
		KEEP(*(.text.file_update_time))
		. = MAX(., __hot_list + 0xe500);
		KEEP(*(.text.__riscv_sys_pwritev2))
		KEEP(*(.text.down_write))
		. = MAX(., __hot_list + 0xe640);
		KEEP(*(.text.generic_write_checks))
		. = MAX(., __hot_list + 0xe700);
		KEEP(*(.text.generic_perform_write))
		. = MAX(., __hot_list + 0xe8c0);
		KEEP(*(.text.up_write))
		. = MAX(., __hot_list + 0xe940);
		KEEP(*(.text.shmem_file_write_iter))
		. = MAX(., __hot_list + 0xe9c0);
		KEEP(*(.text.vfs_writev))
		. = MAX(., __hot_list + 0xec80);
		KEEP(*(.text.do_iter_readv_writev))
		. = MAX(., __hot_list + 0xee00);
		KEEP(*(.text.riscv_clocksource_rdtime))
		. = MAX(., __hot_list + 0xee40);
		KEEP(*(.text.xas_find_conflict))
		. = MAX(., __hot_list + 0xefc0);
		KEEP(*(.text.copy_iovec_from_user))
		. = MAX(., __hot_list + 0xf040);
		KEEP(*(.text.__bitmap_clear))
		. = MAX(., __hot_list + 0xf0c0);
		KEEP(*(.text.radix_tree_node_rcu_free))
		. = MAX(., __hot_list + 0xf100);
		KEEP(*(.text.xas_init_marks))
		. = MAX(., __hot_list + 0xf200);
		KEEP(*(.text.xas_nomem))
		. = MAX(., __hot_list + 0xf280);
		KEEP(*(.text._find_next_bit))
		. = MAX(., __hot_list + 0xf300);
		KEEP(*(.text.copy_folio_from_iter_atomic))
		. = MAX(., __hot_list + 0xfa80);
		KEEP(*(.text.iov_iter_advance))
		. = MAX(., __hot_list + 0xfbc0);
		KEEP(*(.text.iov_iter_revert))
		. = MAX(., __hot_list + 0xfcc0);
		KEEP(*(.text.xas_create))
		. = MAX(., __hot_list + 0x10180);
		KEEP(*(.text.fault_in_iov_iter_readable))
		. = MAX(., __hot_list + 0x10240);
		KEEP(*(.text.xas_free_nodes))
		. = MAX(., __hot_list + 0x10340);
		KEEP(*(.text.xas_store))
		. = MAX(., __hot_list + 0x107c0);
		KEEP(*(.text.import_iovec))
		. = MAX(., __hot_list + 0x109c0);
		KEEP(*(.text.shmem_getattr))
		. = MAX(., __hot_list + 0x10a80);
		KEEP(*(.text.vfs_getattr_nosec))
		. = MAX(., __hot_list + 0x10b40);
		KEEP(*(.text.generic_fillattr))
		. = MAX(., __hot_list + 0x10c80);
		KEEP(*(.text.__riscv_sys_newfstatat))
		. = MAX(., __hot_list + 0x10cc0);
		KEEP(*(.text.fill_mg_cmtime))
		. = MAX(., __hot_list + 0x10d40);
		KEEP(*(.text.path_put))
		. = MAX(., __hot_list + 0x10d80);
		KEEP(*(.text.filename_lookup))
		. = MAX(., __hot_list + 0x10e80);
		KEEP(*(.text.cp_new_stat))
		. = MAX(., __hot_list + 0x10f80);
		KEEP(*(.text.path_lookupat))
		. = MAX(., __hot_list + 0x112c0);
		KEEP(*(.text._copy_to_user))
		. = MAX(., __hot_list + 0x11300);
		KEEP(*(.text.__riscv_sys_close))
		. = MAX(., __hot_list + 0x113c0);
		KEEP(*(.text.module_put))
		. = MAX(., __hot_list + 0x11400);
		KEEP(*(.text.locks_remove_file))
		. = MAX(., __hot_list + 0x11800);
		KEEP(*(.text.shmem_file_open))
		. = MAX(., __hot_list + 0x11840);
		KEEP(*(.text.fput_close_sync))
		. = MAX(., __hot_list + 0x11900);
		KEEP(*(.text.__fput))
		. = MAX(., __hot_list + 0x11b40);
		KEEP(*(.text.locks_remove_posix))
		. = MAX(., __hot_list + 0x11c40);
		KEEP(*(.text.dnotify_flush))
		. = MAX(., __hot_list + 0x11dc0);
		KEEP(*(.text.file_close_fd))
		. = MAX(., __hot_list + 0x11e80);
		KEEP(*(.text.file_f_owner_release))
		. = MAX(., __hot_list + 0x11ec0);
		KEEP(*(.text.generic_file_open))
		. = MAX(., __hot_list + 0x11f00);
		KEEP(*(.text.try_module_get))
		. = MAX(., __hot_list + 0x11f40);
		KEEP(*(.text.alloc_fd))
		. = MAX(., __hot_list + 0x12100);
		KEEP(*(.text.file_ra_state_init))
		. = MAX(., __hot_list + 0x12140);
		KEEP(*(.text.fd_install))
		. = MAX(., __hot_list + 0x121c0);
		KEEP(*(.text.mntget))
		. = MAX(., __hot_list + 0x12200);
		KEEP(*(.text.do_dentry_open))
		. = MAX(., __hot_list + 0x12580);
		KEEP(*(.text.generic_permission))
		. = MAX(., __hot_list + 0x12800);
		KEEP(*(.text.may_open))
		. = MAX(., __hot_list + 0x12a00);
		KEEP(*(.text.alloc_empty_file))
		. = MAX(., __hot_list + 0x12b40);
		KEEP(*(.text.__riscv_sys_openat))
		. = MAX(., __hot_list + 0x12d40);
		KEEP(*(.text.vfs_open))
		. = MAX(., __hot_list + 0x12e00);
		KEEP(*(.text.path_openat))
		. = MAX(., __hot_list + 0x13580);
		KEEP(*(.text.path_get))
		. = MAX(., __hot_list + 0x135c0);
		KEEP(*(.text.do_file_open))
		. = MAX(., __hot_list + 0x136c0);
		KEEP(*(.text.get_unused_fd_flags))
		. = MAX(., __hot_list + 0x13700);
		KEEP(*(.text.mutex_init_generic))
		. = MAX(., __hot_list + 0x13740);
		KEEP(*(.text.errseq_sample))
		. = MAX(., __hot_list + 0x13780);
		KEEP(*(.text.__riscv_sys_getppid))
		. = MAX(., __hot_list + 0x137c0);
		KEEP(*(.text.__task_pid_nr_ns))
		. = MAX(., __hot_list + 0x13880);
		KEEP(*(.text.pte_offset_map_lock))
		. = MAX(., __hot_list + 0x13980);
		KEEP(*(.text.split_large_buddy))
		. = MAX(., __hot_list + 0x13fc0);
		KEEP(*(.text.up_read))
		. = MAX(., __hot_list + 0x14040);
		KEEP(*(.text.prep_new_page))
		. = MAX(., __hot_list + 0x141c0);
		KEEP(*(.text.__alloc_frozen_pages_noprof))
		. = MAX(., __hot_list + 0x14400);
		KEEP(*(.text.get_page_from_freelist))
		. = MAX(., __hot_list + 0x16ac0);
		KEEP(*(.text.p4d_offset))
		. = MAX(., __hot_list + 0x16b00);
		KEEP(*(.text.pud_offset))
		. = MAX(., __hot_list + 0x16b40);
		KEEP(*(.text.free_one_page.constprop.1))
		. = MAX(., __hot_list + 0x16c40);
		KEEP(*(.text.handle_page_fault))
		. = MAX(., __hot_list + 0x16f40);
		KEEP(*(.text.__handle_mm_fault))
		. = MAX(., __hot_list + 0x17d40);
		KEEP(*(.text.pte_offset_map_rw_nolock))
		. = MAX(., __hot_list + 0x17dc0);
		KEEP(*(.text.handle_mm_fault))
		. = MAX(., __hot_list + 0x17f00);
		KEEP(*(.text.lock_mm_and_find_vma))
		. = MAX(., __hot_list + 0x18040);
		KEEP(*(.text.find_vma))
		. = MAX(., __hot_list + 0x18080);
		KEEP(*(.text.down_read_trylock))
		. = MAX(., __hot_list + 0x18100);
		KEEP(*(.text.folio_add_lru_vma))
		. = MAX(., __hot_list + 0x18140);
		KEEP(*(.text.map_anon_folio_pte_nopf))
		. = MAX(., __hot_list + 0x18340);
		KEEP(*(.text.__vmf_anon_prepare))
		. = MAX(., __hot_list + 0x183c0);
		KEEP(*(.text.folio_add_lru))
		. = MAX(., __hot_list + 0x184c0);
		KEEP(*(.text.__folio_alloc_noprof))
		. = MAX(., __hot_list + 0x18680);
		KEEP(*(.text.folio_add_new_anon_rmap))
		. = MAX(., __hot_list + 0x187c0);
		KEEP(*(.text.pte_mkwrite))
		. = MAX(., __hot_list + 0x18800);
		KEEP(*(.text.folio_remove_rmap_ptes))
		. = MAX(., __hot_list + 0x188c0);
		KEEP(*(.text.__tlb_remove_folio_pages))
		. = MAX(., __hot_list + 0x18980);
		KEEP(*(.text.__page_cache_release.part.0))
		. = MAX(., __hot_list + 0x18d00);
		KEEP(*(.text.lru_add))
		. = MAX(., __hot_list + 0x19080);
		KEEP(*(.text.mt_find))
		. = MAX(., __hot_list + 0x19400);
		KEEP(*(.text.mas_next_slot))
		. = MAX(., __hot_list + 0x19ec0);
		KEEP(*(.text.mas_state_walk.constprop.0.isra.0))
		. = MAX(., __hot_list + 0x1a140);
		KEEP(*(.text.flush_icache_pte))
		. = MAX(., __hot_list + 0x1a1c0);
		KEEP(*(.text.mas_ascend))
		. = MAX(., __hot_list + 0x1a340);
		KEEP(*(.text.ktime_get))
		. = MAX(., __hot_list + 0x1a480);
		KEEP(*(.text.arch_sync_dma_for_cpu))
		. = MAX(., __hot_list + 0x1a500);
		KEEP(*(.text.arch_sync_dma_for_device))
		. = MAX(., __hot_list + 0x1a580);
		KEEP(*(.text.ndesc_set_addr))
		. = MAX(., __hot_list + 0x1a5c0);
		KEEP(*(.text.ktime_get_update_offsets_now))
		. = MAX(., __hot_list + 0x1a780);
		KEEP(*(.text.next_uptodate_folio))
		. = MAX(., __hot_list + 0x1a980);
		KEEP(*(.text.set_pte_range))
		. = MAX(., __hot_list + 0x1ab40);
		KEEP(*(.text.folio_add_file_rmap_ptes))
		. = MAX(., __hot_list + 0x1ac40);
		KEEP(*(.text.resched_curr_lazy))
		. = MAX(., __hot_list + 0x1ac80);
		KEEP(*(.text.riscv_clock_next_event))
		. = MAX(., __hot_list + 0x1acc0);
		KEEP(*(.text.clockevents_program_event))
		. = MAX(., __hot_list + 0x1ae00);
		KEEP(*(.text.tick_program_event))
		. = MAX(., __hot_list + 0x1ae80);
		KEEP(*(.text.ndesc_get_tx_status))
		. = MAX(., __hot_list + 0x1af40);
		KEEP(*(.text.dma_unmap_page_attrs))
		. = MAX(., __hot_list + 0x1b080);
		KEEP(*(.text.dma_unmap_phys))
		. = MAX(., __hot_list + 0x1b1c0);
		KEEP(*(.text.ndesc_release_tx_desc))
		. = MAX(., __hot_list + 0x1b200);
		KEEP(*(.text.ndesc_prepare_tx_desc))
		. = MAX(., __hot_list + 0x1b280);
		KEEP(*(.text.dma_map_page_attrs))
		. = MAX(., __hot_list + 0x1b300);
		KEEP(*(.text.dma_direct_map_phys))
		. = MAX(., __hot_list + 0x1b500);
		KEEP(*(.text.clean_desc3))
		. = MAX(., __hot_list + 0x1b580);
		KEEP(*(.text.dma_map_phys))
		. = MAX(., __hot_list + 0x1b5c0);
		KEEP(*(.text.skb_release_data))
		. = MAX(., __hot_list + 0x1b740);
		KEEP(*(.text.skb_free_head))
		. = MAX(., __hot_list + 0x1b800);
		KEEP(*(.text.__local_bh_enable_ip))
		. = MAX(., __hot_list + 0x1b8c0);
		KEEP(*(.text.skb_release_head_state))
		. = MAX(., __hot_list + 0x1b9c0);
		KEEP(*(.text.kfree_skbmem))
		. = MAX(., __hot_list + 0x1ba40);
		KEEP(*(.text.is_vmalloc_addr))
		. = MAX(., __hot_list + 0x1ba80);
		KEEP(*(.text.idle_cpu))
		. = MAX(., __hot_list + 0x1bac0);
		KEEP(*(.text.generic_handle_domain_irq))
		. = MAX(., __hot_list + 0x1bb00);
		KEEP(*(.text.__irq_resolve_mapping))
		. = MAX(., __hot_list + 0x1bb80);
		KEEP(*(.text.add_interrupt_randomness))
		. = MAX(., __hot_list + 0x1bd40);
		KEEP(*(.text.irq_exit_rcu))
		. = MAX(., __hot_list + 0x1be40);
		KEEP(*(.text.riscv_intc_irq_eoi))
		. = MAX(., __hot_list + 0x1be80);
		KEEP(*(.text.riscv_intc_irq))
		. = MAX(., __hot_list + 0x1bf00);
		KEEP(*(.text.irq_enter_rcu))
		. = MAX(., __hot_list + 0x1bf80);
		KEEP(*(.text.__hrtimer_run_queues.constprop.0))
		. = MAX(., __hot_list + 0x1c1c0);
		KEEP(*(.text.__hrtimer_cb_get_time))
		. = MAX(., __hot_list + 0x1c240);
		KEEP(*(.text.riscv_timer_interrupt))
		. = MAX(., __hot_list + 0x1c280);
		KEEP(*(.text.hrtimer_interrupt))
		. = MAX(., __hot_list + 0x1c5c0);
		KEEP(*(.text.handle_percpu_devid_irq))
		. = MAX(., __hot_list + 0x1c700);
		KEEP(*(.text.__hrtimer_rearm_deferred))
		. = MAX(., __hot_list + 0x1c800);
		KEEP(*(.text.kmem_cache_alloc_node_noprof))
		. = MAX(., __hot_list + 0x1c980);
		KEEP(*(.text.hrtimer_start_range_ns_common))
		. = MAX(., __hot_list + 0x1cc80);
		KEEP(*(.text.hrtimer_active))
		KEEP(*(.text.ttwu_do_activate.constprop.0))
		. = MAX(., __hot_list + 0x1cd00);
		KEEP(*(.text.kmalloc_reserve))
		. = MAX(., __hot_list + 0x1ce40);
		KEEP(*(.text.__alloc_skb))
		. = MAX(., __hot_list + 0x1d040);
		KEEP(*(.text.__copy_skb_header))
		. = MAX(., __hot_list + 0x1d1c0);
		KEEP(*(.text.wake_up_process))
		. = MAX(., __hot_list + 0x1d200);
		KEEP(*(.text.sk_free))
		. = MAX(., __hot_list + 0x1d240);
		KEEP(*(.text.sk_skb_reason_drop))
		. = MAX(., __hot_list + 0x1d3c0);
		KEEP(*(.text.stmmac_xmit))
		. = MAX(., __hot_list + 0x1eb00);
		KEEP(*(.text.ndesc_set_tx_owner))
		. = MAX(., __hot_list + 0x1eb40);
		KEEP(*(.text.dev_kfree_skb_any_reason))
		. = MAX(., __hot_list + 0x1ec00);
		KEEP(*(.text.dwmac_enable_dma_transmission))
		. = MAX(., __hot_list + 0x1ec40);
		KEEP(*(.text.tcp_wfree))
		. = MAX(., __hot_list + 0x1edc0);
		KEEP(*(.text.skb_zerocopy_clone))
		. = MAX(., __hot_list + 0x1eec0);
		KEEP(*(.text.ip_send_check))
		. = MAX(., __hot_list + 0x1ef00);
		KEEP(*(.text.get_timespec64))
		. = MAX(., __hot_list + 0x1ef80);
		KEEP(*(.text.hrtimer_start_range_ns_user))
		. = MAX(., __hot_list + 0x1f140);
		KEEP(*(.text.hrtimer_nanosleep))
		. = MAX(., __hot_list + 0x1f2c0);
		KEEP(*(.text.__riscv_sys_nanosleep))
		KEEP(*(.text.do_nanosleep))
		. = MAX(., __hot_list + 0x1f380);
		KEEP(*(.text.hrtimer_wakeup))
		. = MAX(., __hot_list + 0x1f3c0);
		KEEP(*(.text.hrtimer_reprogram.constprop.0))
		. = MAX(., __hot_list + 0x1f440);
		KEEP(*(.text.vm_normal_page))
		. = MAX(., __hot_list + 0x1f4c0);
		KEEP(*(.text.ndesc_get_rx_status))
		. = MAX(., __hot_list + 0x1f5c0);
		KEEP(*(.text.__usecs_to_jiffies))
		. = MAX(., __hot_list + 0x1f600);
		KEEP(*(.text.ipv4_dst_check))
		. = MAX(., __hot_list + 0x1f680);
		KEEP(*(.text.eth_header_parse_protocol))
		. = MAX(., __hot_list + 0x1f6c0);
		KEEP(*(.text.skb_put))
		. = MAX(., __hot_list + 0x1f740);
		KEEP(*(.text.ktime_get_with_offset))
		. = MAX(., __hot_list + 0x1f8c0);
		KEEP(*(.text.__dma_sync_single_for_cpu))
		. = MAX(., __hot_list + 0x1f980);
		KEEP(*(.text.__dma_sync_single_for_device))
		. = MAX(., __hot_list + 0x1fa40);
		KEEP(*(.text.sk_filter_trim_cap))
		. = MAX(., __hot_list + 0x1fcc0);
		KEEP(*(.text.__inet_lookup_established))
		. = MAX(., __hot_list + 0x1fec0);
		KEEP(*(.text.ndesc_set_rx_owner))
		. = MAX(., __hot_list + 0x1ff00);
		KEEP(*(.text.ndesc_get_rx_frame_len))
		. = MAX(., __hot_list + 0x1ff40);
		KEEP(*(.text.inet_gro_receive))
		. = MAX(., __hot_list + 0x20140);
		KEEP(*(.text.tcp_inbound_hash))
		. = MAX(., __hot_list + 0x20180);
		KEEP(*(.text.napi_build_skb))
		. = MAX(., __hot_list + 0x202c0);
		KEEP(*(.text.tcp4_gro_receive))
		. = MAX(., __hot_list + 0x20680);
		KEEP(*(.text.gro_receive_skb))
		. = MAX(., __hot_list + 0x20800);
		KEEP(*(.text.refill_desc3))
		. = MAX(., __hot_list + 0x20880);
		KEEP(*(.text.tcp_gro_receive))
		. = MAX(., __hot_list + 0x20d40);
		KEEP(*(.text.tcp_v4_rcv))
		. = MAX(., __hot_list + 0x21940);
		KEEP(*(.text.page_pool_alloc_pages))
		. = MAX(., __hot_list + 0x219c0);
		KEEP(*(.text.stmmac_rx_buf1_len))
		. = MAX(., __hot_list + 0x21ac0);
		KEEP(*(.text.tcp_v4_fill_cb))
		. = MAX(., __hot_list + 0x21b80);
		KEEP(*(.text.ip_local_deliver))
		. = MAX(., __hot_list + 0x21cc0);
		KEEP(*(.text.dev_gro_receive))
		. = MAX(., __hot_list + 0x22540);
		KEEP(*(.text.ip_rcv_core))
		. = MAX(., __hot_list + 0x227c0);
		KEEP(*(.text.ip_local_deliver_finish))
		. = MAX(., __hot_list + 0x228c0);
		KEEP(*(.text.ip_rcv_finish_core))
		. = MAX(., __hot_list + 0x22cc0);
		KEEP(*(.text.__build_skb_around))
		. = MAX(., __hot_list + 0x22dc0);
		KEEP(*(.text.stmmac_get_rx_hwtstamp))
		. = MAX(., __hot_list + 0x22e80);
		KEEP(*(.text.ip_protocol_deliver_rcu))
		. = MAX(., __hot_list + 0x22f80);
		KEEP(*(.text.raw_local_deliver))
		. = MAX(., __hot_list + 0x23240);
		KEEP(*(.text.eth_type_trans))
		. = MAX(., __hot_list + 0x23380);
		KEEP(*(.text.page_pool_put_unrefed_netmem))
		. = MAX(., __hot_list + 0x23600);
		KEEP(*(.text.napi_pp_put_page))
		. = MAX(., __hot_list + 0x236c0);
		KEEP(*(.text.__netif_receive_skb_core.constprop.0))
		. = MAX(., __hot_list + 0x23e00);
		KEEP(*(.text.pskb_trim_rcsum_slow))
		. = MAX(., __hot_list + 0x23f00);
		KEEP(*(.text.skb_condense))
		. = MAX(., __hot_list + 0x23f80);
		KEEP(*(.text.tcp_add_backlog))
		. = MAX(., __hot_list + 0x24380);
		KEEP(*(.text.anon_rmap_tree_insert))
		. = MAX(., __hot_list + 0x24440);
		KEEP(*(.text.anon_rmap_tree_remove))
		. = MAX(., __hot_list + 0x24640);
		KEEP(*(.text.hrtimer_try_to_cancel))
		. = MAX(., __hot_list + 0x24700);
		KEEP(*(.text.kfree_skb_partial))
		. = MAX(., __hot_list + 0x24780);
		KEEP(*(.text.skb_try_coalesce))
		. = MAX(., __hot_list + 0x24c40);
		KEEP(*(.text.__napi_poll))
		. = MAX(., __hot_list + 0x24e80);
		KEEP(*(.text.napi_complete_done))
		. = MAX(., __hot_list + 0x25080);
		KEEP(*(.text.handle_softirqs))
		. = MAX(., __hot_list + 0x25240);
		KEEP(*(.text.timerqueue_linked_add))
		. = MAX(., __hot_list + 0x25300);
		KEEP(*(.text.sbi_set_timer))
		. = MAX(., __hot_list + 0x25340);
		KEEP(*(.text.___ratelimit))
		. = MAX(., __hot_list + 0x254c0);
		KEEP(*(.text.rcuref_get_slowpath))
		. = MAX(., __hot_list + 0x25540);
		KEEP(*(.text.rb_erase_linked))
		. = MAX(., __hot_list + 0x25580);
		KEEP(*(.text.radix_tree_lookup))
		. = MAX(., __hot_list + 0x25600);
		KEEP(*(.text.rb_insert_color))
		. = MAX(., __hot_list + 0x256c0);
		KEEP(*(.text.rb_erase))
		. = MAX(., __hot_list + 0x25900);
		KEEP(*(.text.memcmp))
		. = MAX(., __hot_list + 0x25940);
		KEEP(*(.text.csum_tcpudp_nofold))
		. = MAX(., __hot_list + 0x25980);
		KEEP(*(.text._copy_from_user))
		. = MAX(., __hot_list + 0x25a00);
		KEEP(*(.text.skb_gro_receive))
		. = MAX(., __hot_list + 0x25e80);
		KEEP(*(.text.napi_skb_free_stolen_head))
		. = MAX(., __hot_list + 0x25fc0);
		KEEP(*(.text.fput))
		. = MAX(., __hot_list + 0x26180);
		KEEP(*(.text.call_rcu))
		. = MAX(., __hot_list + 0x261c0);
		KEEP(*(.text.__skb_datagram_iter.constprop.0))
		. = MAX(., __hot_list + 0x26440);
		KEEP(*(.text.skb_push))
		. = MAX(., __hot_list + 0x26480);
		KEEP(*(.text.skb_pull))
		. = MAX(., __hot_list + 0x264c0);
		KEEP(*(.text.do_csum))
		. = MAX(., __hot_list + 0x26600);
		KEEP(*(.text.sdhci_adma_write_desc))
		. = MAX(., __hot_list + 0x26640);
		KEEP(*(.text.dwcmshc_adma_write_desc))
		. = MAX(., __hot_list + 0x26700);
		KEEP(*(.text.skb_checksum))
		. = MAX(., __hot_list + 0x26980);
		KEEP(*(.text.complete))
		KEEP(*(.text.consume_skb))
		. = MAX(., __hot_list + 0x26a40);
		KEEP(*(.text.skb_queue_tail))
		. = MAX(., __hot_list + 0x26a80);
		KEEP(*(.text.skb_ensure_writable))
		. = MAX(., __hot_list + 0x26b80);
		KEEP(*(.text.skb_checksum_help))
		. = MAX(., __hot_list + 0x26d40);
		KEEP(*(.text.mod_timer))
		. = MAX(., __hot_list + 0x26fc0);
		KEEP(*(.text.lock_timer_base))
		. = MAX(., __hot_list + 0x27080);
		KEEP(*(.text.detach_if_pending))
		. = MAX(., __hot_list + 0x27100);
		KEEP(*(.text.sk_reset_timer))
		. = MAX(., __hot_list + 0x27180);
		KEEP(*(.text.update_curr_common))
		. = MAX(., __hot_list + 0x27200);
		KEEP(*(.text.csum_partial))
		. = MAX(., __hot_list + 0x27240);
		KEEP(*(.text.skb_copy_datagram_iter))
		. = MAX(., __hot_list + 0x27280);
		KEEP(*(.text.tcp_rcv_space_adjust))
		. = MAX(., __hot_list + 0x27380);
		KEEP(*(.text.tcp_v4_do_rcv))
		. = MAX(., __hot_list + 0x27580);
		KEEP(*(.text.tcp_rcv_established))
		. = MAX(., __hot_list + 0x27f00);
		KEEP(*(.text.__tcp_ack_snd_check))
		. = MAX(., __hot_list + 0x280c0);
		KEEP(*(.text.tcp_data_ecn_check))
		. = MAX(., __hot_list + 0x28200);
		KEEP(*(.text.sock_rfree))
		. = MAX(., __hot_list + 0x28280);
		KEEP(*(.text.skb_attempt_defer_free))
		. = MAX(., __hot_list + 0x28380);
		KEEP(*(.text.__kmalloc_node_track_caller_noprof))
		. = MAX(., __hot_list + 0x28540);
		KEEP(*(.text.kmalloc_size_roundup))
		. = MAX(., __hot_list + 0x285c0);
		KEEP(*(.text.tcp_grow_window))
		. = MAX(., __hot_list + 0x28700);
		KEEP(*(.text.__netdev_alloc_skb))
		. = MAX(., __hot_list + 0x28a00);
		KEEP(*(.text.netif_receive_skb))
		. = MAX(., __hot_list + 0x28b00);
		KEEP(*(.text.ip_rcv))
		. = MAX(., __hot_list + 0x28c00);
		KEEP(*(.text.__skb_checksum_complete))
		. = MAX(., __hot_list + 0x28cc0);
		KEEP(*(.text.sock_def_readable))
		. = MAX(., __hot_list + 0x28d40);
		KEEP(*(.text.__tcp_select_window))
		. = MAX(., __hot_list + 0x28f80);
		KEEP(*(.text.tcp_queue_rcv))
		. = MAX(., __hot_list + 0x29100);
		KEEP(*(.text.tcp_data_ready))
		. = MAX(., __hot_list + 0x29180);
		KEEP(*(.text.tcp_event_data_recv))
		. = MAX(., __hot_list + 0x294c0);
		KEEP(*(.text.ep_poll_callback))
		. = MAX(., __hot_list + 0x29740);
		KEEP(*(.text.tcp_send_delayed_ack))
		. = MAX(., __hot_list + 0x298c0);
		KEEP(*(.text.tcp_ack))
		. = MAX(., __hot_list + 0x2b140);
		KEEP(*(.text.tcp_data_queue))
		. = MAX(., __hot_list + 0x2bf00);
		KEEP(*(.text.tcp_urg))
		. = MAX(., __hot_list + 0x2c0c0);
		KEEP(*(.text.tcp_try_rmem_schedule))
		. = MAX(., __hot_list + 0x2cb80);
		KEEP(*(.text.tcp_leave_memory_pressure))
		. = MAX(., __hot_list + 0x2cc00);
		KEEP(*(.text.__sk_mem_schedule))
		. = MAX(., __hot_list + 0x2ce80);
		KEEP(*(.text.__sk_mem_raise_allocated))
		. = MAX(., __hot_list + 0x2d100);
		KEEP(*(.text.__sk_mem_reduce_allocated.part.0))
		. = MAX(., __hot_list + 0x2d180);
		KEEP(*(.text.__init_swait_queue_head))
		. = MAX(., __hot_list + 0x2d1c0);
		KEEP(*(.text.led_trigger_event))
		. = MAX(., __hot_list + 0x2d240);
		KEEP(*(.text.sdhci_led_control))
		. = MAX(., __hot_list + 0x2d300);
		KEEP(*(.text.led_set_brightness))
		. = MAX(., __hot_list + 0x2d3c0);
		KEEP(*(.text.handle_irq_event))
		. = MAX(., __hot_list + 0x2d440);
		KEEP(*(.text.note_interrupt))
		. = MAX(., __hot_list + 0x2d740);
		KEEP(*(.text.handle_fasteoi_irq))
		. = MAX(., __hot_list + 0x2d940);
		KEEP(*(.text.plic_irq_eoi))
		. = MAX(., __hot_list + 0x2da80);
		KEEP(*(.text.__handle_irq_event_percpu))
		. = MAX(., __hot_list + 0x2dc00);
		KEEP(*(.text.sdhci_irq))
		. = MAX(., __hot_list + 0x2e740);
		KEEP(*(.text.plic_handle_irq))
		. = MAX(., __hot_list + 0x2e8c0);
		KEEP(*(.text.update_rt_rq_load_avg))
		. = MAX(., __hot_list + 0x2eb40);
		KEEP(*(.text.calc_wheel_index))
		. = MAX(., __hot_list + 0x2ec80);
		KEEP(*(.text.enqueue_timer))
		. = MAX(., __hot_list + 0x2ed00);
		KEEP(*(.text.dequeue_rt_stack))
		. = MAX(., __hot_list + 0x2f000);
		KEEP(*(.text.sg_miter_stop))
		. = MAX(., __hot_list + 0x2f0c0);
		KEEP(*(.text.minmax_running_min))
		. = MAX(., __hot_list + 0x2f180);
		KEEP(*(.text.sg_miter_next))
		. = MAX(., __hot_list + 0x2f2c0);
		KEEP(*(.text.rb_replace_node))
		. = MAX(., __hot_list + 0x2f300);
		KEEP(*(.text.udelay))
		. = MAX(., __hot_list + 0x2f3c0);
		KEEP(*:arch/riscv/lib/memmove.o(.text))
		/*
		 * stat() of a missing name: the slow lookup and the negative dentry, not in the measured
		 * order. Its slot is one I-cache way (16 KiB), so the code after it keeps its cache sets; the
		 * offset in the slot is measured.
		 */
		__hot_missing = .;
		. = MAX(., __hot_missing + 0x1c0);
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
