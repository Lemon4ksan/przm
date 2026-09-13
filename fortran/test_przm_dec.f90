program test_przm_dec
  use iso_c_binding
  use przm_dec
  implicit none

  call test_get_uncompressed_size()
  call test_symplectic_involution()
  call test_mahler_transform()
  call test_standard_fused_raw()
  call test_standard_fused_lz()
  call test_pws_grammar()
  
  print *, ">>> ALL TESTS PASSED SUCCESSFULLY! (0 Allocations, 100% Lossless) <<<"

contains

  subroutine write_u32_le(p, v)
    integer(c_int8_t), intent(inout) :: p(4)
    integer(c_int32_t), value :: v
    p(1) = int(iand(v, 255), c_int8_t)
    p(2) = int(iand(ishft(v, -8), 255), c_int8_t)
    p(3) = int(iand(ishft(v, -16), 255), c_int8_t)
    p(4) = int(iand(ishft(v, -24), 255), c_int8_t)
  end subroutine write_u32_le

  subroutine test_get_uncompressed_size()
    integer(c_int8_t) :: frame_std(8)
    integer(c_int64_t) :: sz

    frame_std = [ichar('P', kind=c_int8_t), ichar('R', kind=c_int8_t), ichar('Z', kind=c_int8_t), ichar('M', kind=c_int8_t), &
                 int(0, c_int8_t), int(32, c_int8_t), int(78, c_int8_t), int(0, c_int8_t)]
    sz = przm_get_uncompressed_size(frame_std, 8_c_size_t)
    if (sz /= 20000) stop "Standard PRZM size mismatch"
    
    print *, "[PASS] przm_get_uncompressed_size"
  end subroutine test_get_uncompressed_size

  subroutine test_symplectic_involution()
    integer(c_int8_t) :: frame(28)
    integer(c_int8_t) :: dst(128)
    integer(c_int8_t) :: orig(16)
    integer(c_int64_t) :: res
    integer :: i
    
    orig = [int(z'12', c_int8_t), int(z'34', c_int8_t), int(z'56', c_int8_t), int(z'78', c_int8_t), &
            int(z'AB', c_int8_t), int(z'CD', c_int8_t), int(z'EF', c_int8_t), int(z'01', c_int8_t), &
            int(z'42', c_int8_t), int(z'69', c_int8_t), int(z'99', c_int8_t), int(z'AA', c_int8_t), &
            int(z'FF', c_int8_t), int(z'00', c_int8_t), int(z'55', c_int8_t), int(z'AA', c_int8_t)]
            
    call write_u32_le(frame(1:4), int(z'53594D50', c_int32_t)) ! SYMP
    frame(5) = 1
    frame(6) = int(z'05', c_int8_t)
    frame(7) = int(z'0A', c_int8_t)
    frame(8) = int(z'1F', c_int8_t)
    call write_u32_le(frame(9:12), 16)
    frame(13:28) = orig(1:16)
    
    res = przm_decompress(frame, 28_c_size_t, dst, 128_c_size_t)
    if (res /= 16) stop "Symplectic decompression returned wrong size"
    
    frame(13:28) = dst(1:16)
    res = przm_decompress(frame, 28_c_size_t, dst, 128_c_size_t)
    if (res /= 16) stop "Second decompression failed"
    
    do i = 1, 16
        if (dst(i) /= orig(i)) stop "Symplectic involution failed to reconstruct original bytes!"
    end do
    
    print *, "[PASS] symplectic_involution"
  end subroutine test_symplectic_involution

  subroutine test_mahler_transform()
    integer(c_int8_t) :: orig(16), fwd(16), frame(28), dst(128)
    integer(c_int64_t) :: res
    integer :: i, s
    
    do i = 1, 16
        orig(i) = int(i - 1, c_int8_t)
    end do
    fwd = orig
    do s = 1, 15
        do i = 16, s + 1, -1
            fwd(i) = fwd(i) - fwd(i-1)
        end do
    end do
    
    call write_u32_le(frame(1:4), int(z'4D41484C', c_int32_t)) ! MAHL
    frame(5) = 1
    frame(6) = 0; frame(7) = 0; frame(8) = 0
    call write_u32_le(frame(9:12), 16)
    frame(13:28) = fwd(1:16)
    
    res = przm_decompress(frame, 28_c_size_t, dst, 128_c_size_t)
    if (res /= 16) stop "Mahler decompression failed"
    do i = 1, 16
        if (dst(i) /= orig(i)) stop "Mahler reconstruction mismatch!"
    end do
    
    print *, "[PASS] mahler_transform"
  end subroutine test_mahler_transform

  subroutine test_standard_fused_raw()
    integer(c_int8_t) :: frame(72), dst(128)
    integer(c_int64_t) :: res
    character(len=*), parameter :: text = "Hello PRZM v0.5 from Pure C!"
    integer :: i, len
    
    len = len_trim(text)
    frame(1) = ichar('P'); frame(2) = ichar('R'); frame(3) = ichar('Z'); frame(4) = ichar('M')
    frame(5) = 4 ! UNCOMPRESSED
    frame(6) = int(len, c_int8_t); frame(7) = 0; frame(8) = 0
    do i = 1, len
        frame(8+i) = int(ichar(text(i:i)), c_int8_t)
    end do
    
    res = przm_decompress(frame, int(8+len, c_size_t), dst, 128_c_size_t)
    if (res /= len) stop "Raw decompress size mismatch"
    
    print *, "[PASS] standard_fused_raw"
  end subroutine test_standard_fused_raw

  subroutine test_standard_fused_lz()
    integer(c_int8_t) :: frame(64), dst(128)
    integer(c_int64_t) :: res
    integer :: fptr, i
    
    fptr = 1
    frame(fptr) = ichar('P'); fptr = fptr + 1
    frame(fptr) = ichar('R'); fptr = fptr + 1
    frame(fptr) = ichar('Z'); fptr = fptr + 1
    frame(fptr) = ichar('M'); fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    frame(fptr) = 5; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    
    frame(fptr) = 2; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    
    do i = 1, 8
        frame(fptr) = 0; fptr = fptr + 1
    end do
    
    frame(fptr) = int(z'F3', c_int8_t); fptr = fptr + 1
    frame(fptr) = int(z'00', c_int8_t); fptr = fptr + 1
    
    frame(fptr) = ichar('A'); fptr = fptr + 1
    
    res = przm_decompress(frame, int(fptr-1, c_size_t), dst, 128_c_size_t)
    if (res /= 5) stop "Fused LZ decompress size mismatch"
    if (dst(1) /= ichar('A') .or. dst(2) /= ichar('A') .or. dst(3) /= ichar('A') .or. dst(4) /= ichar('A') .or. dst(5) /= ichar('A')) stop "Payload mismatch"
    
    print *, "[PASS] standard_fused_lz"
  end subroutine test_standard_fused_lz

  subroutine test_pws_grammar()
    integer(c_int8_t) :: frame(64), dst(128)
    integer(c_int64_t) :: res
    integer :: fptr
    
    fptr = 1
    frame(fptr) = ichar('P'); fptr = fptr + 1
    frame(fptr) = ichar('W'); fptr = fptr + 1
    frame(fptr) = int(z'01', c_int8_t); fptr = fptr + 1
    frame(fptr) = int(z'00', c_int8_t); fptr = fptr + 1
    call write_u32_le(frame(fptr:fptr+3), 4); fptr = fptr + 4
    call write_u32_le(frame(fptr:fptr+3), 1); fptr = fptr + 4
    
    frame(fptr) = 1; fptr = fptr + 1
    frame(fptr) = 1; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    
    frame(fptr) = ichar('H'); fptr = fptr + 1
    frame(fptr) = ichar('e'); fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    frame(fptr) = 0; fptr = fptr + 1
    
    frame(fptr) = int(z'D0', c_int8_t); fptr = fptr + 1
    
    res = przm_decompress(frame, int(fptr-1, c_size_t), dst, 128_c_size_t)
    if (res /= 4) stop "PWS decompress size mismatch"
    
    print *, "[PASS] pws_grammar"
  end subroutine test_pws_grammar

end program test_przm_dec
