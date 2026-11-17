/* SPDX-License-Identifier: GPL-2.0-only */
/* Measured order of the hot kernel functions (docs/KERNEL-HOT-TEXT.md). */
		. = ALIGN(. + 62, 64);
		*(.text.riscv_v_context_nesting_end)
		. = ALIGN(. + 62, 64);
		*(.text.riscv_v_context_nesting_start)
		. = ALIGN(. + 62, 64);
		*:arch/riscv/lib/memset.o(.text)
		. = ALIGN(. + 62, 64);
		*:arch/riscv/lib/uaccess.o(.text)
		. = ALIGN(. + 62, 64);
		*:arch/riscv/lib/uaccess_vector.o(.text)
		. = ALIGN(. + 62, 64);
		*(.text.__memcpy)
		. = ALIGN(. + 62, 64);
		*:arch/riscv/lib/memcpy.o(.text)
		. = ALIGN(. + 62, 64);
		*(.text.__rcu_read_unlock)
		. = ALIGN(. + 62, 64);
		*(.text.enter_vector_usercopy)
		. = ALIGN(. + 62, 64);
		*(.text.folio_mark_accessed)
		. = ALIGN(. + 62, 64);
		*(.text.make_vfsuid)
		. = ALIGN(. + 62, 64);
		*(.text.xas_find)
		. = ALIGN(. + 62, 64);
		*(.text.xas_load)
		. = ALIGN(. + 62, 64);
		*(.text.__d_lookup_rcu)
		. = ALIGN(. + 62, 64);
		*(.text.__legitimize_mnt)
		. = ALIGN(. + 62, 64);
		*(.text.__lookup_mnt)
		. = ALIGN(. + 62, 64);
		*(.text._copy_to_iter)
		. = ALIGN(. + 62, 64);
		*(.text._find_next_zero_bit)
		. = ALIGN(. + 62, 64);
		*(.text.complete_walk)
		. = ALIGN(. + 62, 64);
		*(.text.copy_page_to_iter)
		. = ALIGN(. + 62, 64);
		*(.text.dput)
		. = ALIGN(. + 62, 64);
		*(.text.fdget)
		. = ALIGN(. + 62, 64);
		*(.text.filemap_get_entry)
		. = ALIGN(. + 62, 64);
		*(.text.folio_mapping)
		. = ALIGN(. + 62, 64);
		*(.text.folio_unlock)
		. = ALIGN(. + 62, 64);
		*(.text.getname_flags)
		. = ALIGN(. + 62, 64);
		*(.text.inode_to_bdi)
		. = ALIGN(. + 62, 64);
		*(.text.kmem_cache_alloc_noprof)
		. = ALIGN(. + 62, 64);
		*(.text.kmem_cache_free)
		. = ALIGN(. + 62, 64);
		*(.text.ktime_get_coarse_real_ts64_mg)
		. = ALIGN(. + 62, 64);
		*(.text.link_path_walk)
		. = ALIGN(. + 62, 64);
		*(.text.lockref_get)
		. = ALIGN(. + 62, 64);
		*(.text.lockref_get_not_dead)
		. = ALIGN(. + 62, 64);
		*(.text.lockref_put_return)
		. = ALIGN(. + 62, 64);
		*(.text.lookup_fast)
		. = ALIGN(. + 62, 64);
		*(.text.make_vfsgid)
		. = ALIGN(. + 62, 64);
		*(.text.mntput)
		. = ALIGN(. + 62, 64);
		*(.text.nd_jump_root)
		. = ALIGN(. + 62, 64);
		*(.text.path_init)
		. = ALIGN(. + 62, 64);
		*(.text.putname)
		. = ALIGN(. + 62, 64);
		*(.text.set_root)
		. = ALIGN(. + 62, 64);
		*(.text.step_into_slowpath)
		. = ALIGN(. + 62, 64);
		*(.text.strncpy_from_user)
		. = ALIGN(. + 62, 64);
		*(.text.terminate_walk)
		. = ALIGN(. + 62, 64);
		*(.text.try_to_unlazy)
		. = ALIGN(. + 62, 64);
		*(.text.vfs_read.part.0)
		. = ALIGN(. + 62, 64);
		*(.text.prandom_u32_state)
		. = ALIGN(. + 62, 64);
		*(.text.update_curr.constprop.0)
		. = ALIGN(. + 62, 64);
		*(.text.__update_load_avg_cfs_rq)
		. = ALIGN(. + 62, 64);
		*(.text.__update_load_avg_se)
		. = ALIGN(. + 62, 64);
		*(.text.avg_vruntime)
		. = ALIGN(. + 62, 64);
		*(.text.mutex_unlock)
		. = ALIGN(. + 62, 64);
		*(.text.mutex_lock)
		. = ALIGN(. + 62, 64);
		*(.text.update_rq_clock)
		. = ALIGN(. + 62, 64);
		*(.text.dl_server_update)
		. = ALIGN(. + 62, 64);
		*(.text.update_curr_dl_se)
		. = ALIGN(. + 62, 64);
		*(.text.fdget_pos)
		. = ALIGN(. + 62, 64);
		*(.text.kill_fasync)
		. = ALIGN(. + 62, 64);
		*(.text.anon_pipe_trim_and_unlock)
		. = ALIGN(. + 62, 64);
		*(.text.__enqueue_entity)
		. = ALIGN(. + 62, 64);
		*(.text.__dequeue_entity)
		. = ALIGN(. + 62, 64);
		*(.text.reweight_eevdf)
		. = ALIGN(. + 62, 64);
		*(.text.__srcu_read_unlock)
		. = ALIGN(. + 62, 64);
		*(.text.rcu_qs)
		. = ALIGN(. + 62, 64);
		*(.text.migrate_disable_switch.constprop.0)
		. = ALIGN(. + 62, 64);
		*(.text.finish_task_switch.isra.0)
		. = ALIGN(. + 62, 64);
		*(.text.__wake_up_common_lock)
		. = ALIGN(. + 62, 64);
		*(.text.pick_task_fair)
		. = ALIGN(. + 62, 64);
		*(.text.put_prev_task_fair)
		. = ALIGN(. + 62, 64);
		*(.text.set_next_task_fair)
		. = ALIGN(. + 62, 64);
		*(.text.switch_mm)
		. = ALIGN(. + 62, 64);
		*(.text.__riscv_sys_read)
		. = ALIGN(. + 62, 64);
		*(.text.__wake_up_sync_key)
		. = ALIGN(. + 62, 64);
		*(.text.anon_pipe_write)
		. = ALIGN(. + 62, 64);
		*(.text.__riscv_sys_write)
		. = ALIGN(. + 62, 64);
		*(.text.anon_pipe_prefill_and_lock)
		. = ALIGN(. + 62, 64);
		*(.text.vfs_write.part.0)
		. = ALIGN(. + 62, 64);
		*(.text.anon_pipe_read)
		. = ALIGN(. + 62, 64);
		*(.text.wakeup_preempt)
		. = ALIGN(. + 62, 64);
		*(.text.enqueue_task)
		. = ALIGN(. + 62, 64);
		*(.text.wakeup_preempt_fair)
		. = ALIGN(. + 62, 64);
		*(.text.try_to_wake_up)
		. = ALIGN(. + 62, 64);
		*(.text.__dequeue_task)
		. = ALIGN(. + 62, 64);
		*(.text.prepare_to_wait_event)
		. = ALIGN(. + 62, 64);
		*(.text.init_wait_entry)
		. = ALIGN(. + 62, 64);
		*(.text.finish_wait)
		. = ALIGN(. + 62, 64);
		*(.text.default_wake_function)
		. = ALIGN(. + 62, 64);
		*(.text.autoremove_wake_function)
		. = ALIGN(. + 62, 64);
		*(.text.dequeue_task_fair)
		. = ALIGN(. + 62, 64);
		*(.text.enqueue_task_fair)
		. = ALIGN(. + 62, 64);
		*(.text.place_entity)
		. = ALIGN(. + 62, 64);
		*(.text.___task_rq_lock)
		. = ALIGN(. + 62, 64);
		*(.text.requeue_delayed_entity)
		. = ALIGN(. + 62, 64);
		*(.text.rb_next)
		. = ALIGN(. + 62, 64);
		*(.text._copy_from_iter)
		. = ALIGN(. + 62, 64);
		*(.text.__rb_erase_color)
		. = ALIGN(. + 62, 64);
		*(.text.copy_page_from_iter)
		. = ALIGN(. + 62, 64);
		*(.text.touch_atime)
		. = ALIGN(. + 62, 64);
		*(.text.__riscv_sys_pread64)
		. = ALIGN(. + 62, 64);
		*(.text.shmem_file_read_iter)
		*(.text.current_time)
		*(.text.iov_iter_zero)
		*(.text.noop_dirty_folio)
		*(.text.shmem_write_begin)
		*(.text.shmem_write_end)
		*(.text.folio_mark_dirty)
		*(.text.shmem_get_folio_gfp.isra.0)
		*(.text.kfree)
		*(.text.file_remove_privs)
		*(.text.file_update_time)
		*(.text.__riscv_sys_pwritev2)
		*(.text.down_write)
		*(.text.generic_write_checks)
		*(.text.generic_perform_write)
		*(.text.up_write)
		*(.text.shmem_file_write_iter)
		*(.text.vfs_writev)
		*(.text.do_iter_readv_writev)
		*(.text.riscv_clocksource_rdtime)
		*(.text.xas_find_conflict)
		*(.text.copy_iovec_from_user)
		*(.text.__bitmap_clear)
		*(.text.radix_tree_node_rcu_free)
		*(.text.xas_init_marks)
		*(.text.xas_nomem)
		*(.text._find_next_bit)
		*(.text.copy_folio_from_iter_atomic)
		*(.text.iov_iter_advance)
		*(.text.iov_iter_revert)
		*(.text.xas_create)
		*(.text.fault_in_iov_iter_readable)
		*(.text.xas_free_nodes)
		*(.text.xas_store)
		*(.text.import_iovec)
		*(.text.shmem_getattr)
		*(.text.vfs_getattr_nosec)
		*(.text.generic_fillattr)
		*(.text.__riscv_sys_newfstatat)
		*(.text.fill_mg_cmtime)
		*(.text.path_put)
		*(.text.filename_lookup)
		*(.text.cp_new_stat)
		*(.text.path_lookupat)
		*(.text._copy_to_user)
		*(.text.__riscv_sys_close)
		*(.text.module_put)
		*(.text.locks_remove_file)
		*(.text.shmem_file_open)
		*(.text.fput_close_sync)
		*(.text.__fput)
		*(.text.locks_remove_posix)
		*(.text.dnotify_flush)
		*(.text.file_close_fd)
		*(.text.file_f_owner_release)
		*(.text.generic_file_open)
		*(.text.try_module_get)
		*(.text.alloc_fd)
		*(.text.file_ra_state_init)
		*(.text.fd_install)
		*(.text.mntget)
		*(.text.do_dentry_open)
		*(.text.generic_permission)
		*(.text.may_open)
		*(.text.alloc_empty_file)
		*(.text.__riscv_sys_openat)
		*(.text.vfs_open)
		*(.text.path_openat)
		*(.text.path_get)
		*(.text.do_file_open)
		*(.text.get_unused_fd_flags)
		*(.text.mutex_init_generic)
		*(.text.errseq_sample)
		*(.text.__riscv_sys_getppid)
		*(.text.__task_pid_nr_ns)
		*(.text.pte_offset_map_lock)
		*(.text.split_large_buddy)
		*(.text.up_read)
		*(.text.prep_new_page)
		*(.text.__alloc_frozen_pages_noprof)
		*(.text.get_page_from_freelist)
		*(.text.p4d_offset)
		*(.text.pud_offset)
		*(.text.free_one_page.constprop.1)
		*(.text.handle_page_fault)
		*(.text.__handle_mm_fault)
		*(.text.pte_offset_map_rw_nolock)
		*(.text.handle_mm_fault)
		*(.text.lock_mm_and_find_vma)
		*(.text.find_vma)
		*(.text.down_read_trylock)
		*(.text.folio_add_lru_vma)
		*(.text.map_anon_folio_pte_nopf)
		*(.text.__vmf_anon_prepare)
		*(.text.folio_add_lru)
		*(.text.__folio_alloc_noprof)
		*(.text.folio_add_new_anon_rmap)
		*(.text.pte_mkwrite)
		*(.text.folio_remove_rmap_ptes)
		*(.text.__tlb_remove_folio_pages)
		*(.text.__page_cache_release.part.0)
		*(.text.lru_add)
		*(.text.mt_find)
		*(.text.mas_next_slot)
		*(.text.mas_state_walk.constprop.0.isra.0)
		*(.text.flush_icache_pte)
		*(.text.mas_ascend)
		*(.text.ktime_get)
		*(.text.arch_sync_dma_for_cpu)
		*(.text.arch_sync_dma_for_device)
		*(.text.ndesc_set_addr)
		*(.text.ktime_get_update_offsets_now)
		*(.text.next_uptodate_folio)
		*(.text.set_pte_range)
		*(.text.folio_add_file_rmap_ptes)
		*(.text.resched_curr_lazy)
		*(.text.riscv_clock_next_event)
		*(.text.clockevents_program_event)
		*(.text.tick_program_event)
		*(.text.ndesc_get_tx_status)
		*(.text.dma_unmap_page_attrs)
		*(.text.dma_unmap_phys)
		*(.text.ndesc_release_tx_desc)
		*(.text.ndesc_prepare_tx_desc)
		*(.text.dma_map_page_attrs)
		*(.text.dma_direct_map_phys)
		*(.text.clean_desc3)
		*(.text.dma_map_phys)
		*(.text.skb_release_data)
		*(.text.skb_free_head)
		*(.text.__local_bh_enable_ip)
		*(.text.skb_release_head_state)
		*(.text.kfree_skbmem)
		*(.text.is_vmalloc_addr)
		*(.text.idle_cpu)
		*(.text.generic_handle_domain_irq)
		*(.text.__irq_resolve_mapping)
		*(.text.add_interrupt_randomness)
		*(.text.irq_exit_rcu)
		*(.text.riscv_intc_irq_eoi)
		*(.text.riscv_intc_irq)
		*(.text.irq_enter_rcu)
		*(.text.__hrtimer_run_queues.constprop.0)
		*(.text.__hrtimer_cb_get_time)
		*(.text.riscv_timer_interrupt)
		*(.text.hrtimer_interrupt)
		*(.text.handle_percpu_devid_irq)
		*(.text.__hrtimer_rearm_deferred)
		*(.text.kmem_cache_alloc_node_noprof)
		*(.text.hrtimer_start_range_ns_common)
		*(.text.hrtimer_active)
		*(.text.ttwu_do_activate.constprop.0)
		*(.text.kmalloc_reserve)
		*(.text.__alloc_skb)
		*(.text.__copy_skb_header)
		*(.text.wake_up_process)
		*(.text.sk_free)
		*(.text.sk_skb_reason_drop)
		*(.text.stmmac_xmit)
		*(.text.ndesc_set_tx_owner)
		*(.text.dev_kfree_skb_any_reason)
		*(.text.dwmac_enable_dma_transmission)
		*(.text.tcp_wfree)
		*(.text.skb_zerocopy_clone)
		*(.text.ip_send_check)
		*(.text.get_timespec64)
		*(.text.hrtimer_start_range_ns_user)
		*(.text.hrtimer_nanosleep)
		*(.text.__riscv_sys_nanosleep)
		*(.text.do_nanosleep)
		*(.text.hrtimer_wakeup)
		*(.text.hrtimer_reprogram.constprop.0)
		*(.text.vm_normal_page)
		*(.text.ndesc_get_rx_status)
		*(.text.__usecs_to_jiffies)
		*(.text.ipv4_dst_check)
		*(.text.eth_header_parse_protocol)
		*(.text.skb_put)
		*(.text.ktime_get_with_offset)
		*(.text.__dma_sync_single_for_cpu)
		*(.text.__dma_sync_single_for_device)
		*(.text.sk_filter_trim_cap)
		*(.text.__inet_lookup_established)
		*(.text.ndesc_set_rx_owner)
		*(.text.ndesc_get_rx_frame_len)
		*(.text.inet_gro_receive)
		*(.text.tcp_inbound_hash)
		*(.text.napi_build_skb)
		*(.text.tcp4_gro_receive)
		*(.text.gro_receive_skb)
		*(.text.refill_desc3)
		*(.text.tcp_gro_receive)
		*(.text.tcp_v4_rcv)
		*(.text.page_pool_alloc_pages)
		*(.text.stmmac_rx_buf1_len)
		*(.text.tcp_v4_fill_cb)
		*(.text.ip_local_deliver)
		*(.text.dev_gro_receive)
		*(.text.ip_rcv_core)
		*(.text.ip_local_deliver_finish)
		*(.text.ip_rcv_finish_core)
		*(.text.__build_skb_around)
		*(.text.stmmac_get_rx_hwtstamp)
		*(.text.ip_protocol_deliver_rcu)
		*(.text.raw_local_deliver)
		*(.text.eth_type_trans)
		*(.text.page_pool_put_unrefed_netmem)
		*(.text.napi_pp_put_page)
		*(.text.__netif_receive_skb_core.constprop.0)
		*(.text.pskb_trim_rcsum_slow)
		*(.text.skb_condense)
		*(.text.tcp_add_backlog)
		*(.text.anon_rmap_tree_insert)
		*(.text.anon_rmap_tree_remove)
		*(.text.hrtimer_try_to_cancel)
		*(.text.kfree_skb_partial)
		*(.text.skb_try_coalesce)
		*(.text.__napi_poll)
		*(.text.napi_complete_done)
		*(.text.handle_softirqs)
		*(.text.timerqueue_linked_add)
		*(.text.sbi_set_timer)
		*(.text.___ratelimit)
		*(.text.rcuref_get_slowpath)
		*(.text.rb_erase_linked)
		*(.text.radix_tree_lookup)
		*(.text.rb_insert_color)
		*(.text.rb_erase)
		*(.text.memcmp)
		*(.text.csum_tcpudp_nofold)
		*(.text._copy_from_user)
		*(.text.skb_gro_receive)
		*(.text.napi_skb_free_stolen_head)
		*(.text.fput)
		*(.text.call_rcu)
		*(.text.__skb_datagram_iter.constprop.0)
		*(.text.skb_push)
		*(.text.skb_pull)
		*(.text.do_csum)
		*(.text.sdhci_adma_write_desc)
		*(.text.dwcmshc_adma_write_desc)
		*(.text.skb_checksum)
		*(.text.complete)
		*(.text.consume_skb)
		*(.text.skb_queue_tail)
		*(.text.skb_ensure_writable)
		*(.text.skb_checksum_help)
		*(.text.mod_timer)
		*(.text.lock_timer_base)
		*(.text.detach_if_pending)
		*(.text.sk_reset_timer)
		*(.text.update_curr_common)
		*(.text.csum_partial)
		*(.text.skb_copy_datagram_iter)
		*(.text.tcp_rcv_space_adjust)
		*(.text.tcp_v4_do_rcv)
		*(.text.tcp_rcv_established)
		*(.text.__tcp_ack_snd_check)
		*(.text.tcp_data_ecn_check)
		*(.text.sock_rfree)
		*(.text.skb_attempt_defer_free)
		*(.text.__kmalloc_node_track_caller_noprof)
		*(.text.kmalloc_size_roundup)
		*(.text.tcp_grow_window)
		*(.text.__netdev_alloc_skb)
		*(.text.netif_receive_skb)
		*(.text.ip_rcv)
		*(.text.__skb_checksum_complete)
		*(.text.sock_def_readable)
		*(.text.__tcp_select_window)
		*(.text.tcp_queue_rcv)
		*(.text.tcp_data_ready)
		*(.text.tcp_event_data_recv)
		*(.text.ep_poll_callback)
		*(.text.tcp_send_delayed_ack)
		*(.text.tcp_ack)
		*(.text.tcp_data_queue)
		*(.text.tcp_urg)
		*(.text.tcp_try_rmem_schedule)
		*(.text.tcp_leave_memory_pressure)
		*(.text.__sk_mem_schedule)
		*(.text.__sk_mem_raise_allocated)
		*(.text.__sk_mem_reduce_allocated.part.0)
		*(.text.__init_swait_queue_head)
		*(.text.led_trigger_event)
		*(.text.sdhci_led_control)
		*(.text.led_set_brightness)
		*(.text.handle_irq_event)
		*(.text.note_interrupt)
		*(.text.handle_fasteoi_irq)
		*(.text.plic_irq_eoi)
		*(.text.__handle_irq_event_percpu)
		*(.text.sdhci_irq)
		*(.text.plic_handle_irq)
		*(.text.update_rt_rq_load_avg)
		*(.text.calc_wheel_index)
		*(.text.enqueue_timer)
		*(.text.dequeue_rt_stack)
		*(.text.sg_miter_stop)
		*(.text.minmax_running_min)
		*(.text.sg_miter_next)
		*(.text.rb_replace_node)
		*(.text.udelay)
		*:arch/riscv/lib/memmove.o(.text)
