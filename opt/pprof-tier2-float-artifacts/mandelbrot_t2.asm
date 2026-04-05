    0: 0xd10203ff  sub sp, sp, #0x80
    4: 0xa9007bfd  stp x29, x30, [sp]
    8: 0x910003fd  mov x29, sp
   12: 0xa90153f3  stp x19, x20, [sp,#16]
   16: 0xa9025bf5  stp x21, x22, [sp,#32]
   20: 0xa90363f7  stp x23, x24, [sp,#48]
   24: 0xa9046bf9  stp x25, x26, [sp,#64]
   28: 0xa90573fb  stp x27, x28, [sp,#80]
   32: 0x6d0627e8  stp d8, d9, [sp,#96]
   36: 0x6d072fea  stp d10, d11, [sp,#112]
   40: 0xaa0003f3  mov x19, x0
   44: 0xf940027a  ldr x26, [x19]
   48: 0xf940067b  ldr x27, [x19,#8]
   52: 0xd2ffffd8  mov x24, #0xfffe000000000000
   56: 0xd2ffffb9  mov x25, #0xfffd000000000000
   60: 0xf9400354  ldr x20, [x26]
   64: 0xaa1403e0  mov x0, x20
   68: 0xd370fc02  lsr x2, x0, #48
   72: 0xd29fffc3  mov x3, #0xfffe
   76: 0xeb03005f  cmp x2, x3
   80: 0x54000081  b.ne .+0x10
   84: 0xaa0003f5  mov x21, x0
   88: 0xf9006f55  str x21, [x26,#216]
   92: 0x14000004  b .+0x10
   96: 0xd2800040  mov x0, #0x2
  100: 0xf9000a60  str x0, [x19,#16]
  104: 0x1400010b  b .+0x42c
  108: 0xd2800014  mov x20, #0x0
  112: 0xd340be80  ubfx x0, x20, #0, #48
  116: 0xaa180000  orr x0, x0, x24
  120: 0xf9007340  str x0, [x26,#224]
  124: 0xd2800036  mov x22, #0x1
  128: 0x9340bea0  sbfx x0, x21, #0, #48
  132: 0xd1000417  sub x23, x0, #0x1
  136: 0x9340bee0  sbfx x0, x23, #0, #48
  140: 0xeb17001f  cmp x0, x23
  144: 0x54000160  b.eq .+0x2c
  148: 0xf9006f55  str x21, [x26,#216]
  152: 0xd340bec0  ubfx x0, x22, #0, #48
  156: 0xaa180000  orr x0, x0, x24
  160: 0xf9007740  str x0, [x26,#232]
  164: 0xd340be80  ubfx x0, x20, #0, #48
  168: 0xaa180000  orr x0, x0, x24
  172: 0xf9007340  str x0, [x26,#224]
  176: 0xd2800040  mov x0, #0x2
  180: 0xf9000a60  str x0, [x19,#16]
  184: 0x140000f7  b .+0x3dc
  188: 0xd340bee0  ubfx x0, x23, #0, #48
  192: 0xaa180000  orr x0, x0, x24
  196: 0xf9007b40  str x0, [x26,#240]
  200: 0xd2800036  mov x22, #0x1
  204: 0xd340bec0  ubfx x0, x22, #0, #48
  208: 0xaa180000  orr x0, x0, x24
  212: 0xf9007f40  str x0, [x26,#248]
  216: 0x9280001c  mov x28, #0xffffffffffffffff
  220: 0xd340bf80  ubfx x0, x28, #0, #48
  224: 0xaa180000  orr x0, x0, x24
  228: 0xf9008340  str x0, [x26,#256]
  232: 0xd340be80  ubfx x0, x20, #0, #48
  236: 0xaa180000  orr x0, x0, x24
  240: 0xf9014340  str x0, [x26,#640]
  244: 0xaa1c03f5  mov x21, x28
  248: 0x140000d2  b .+0x348
  252: 0xd2e80000  mov x0, #0x4000000000000000
  256: 0x9e670004  fmov d4, x0
  260: 0xf9414b40  ldr x0, [x26,#656]
  264: 0x9340bc00  sbfx x0, x0, #0, #48
  268: 0x9e620001  scvtf d1, x0
  272: 0x1e610885  fmul d5, d4, d1
  276: 0xf9406f40  ldr x0, [x26,#216]
  280: 0x9340bc00  sbfx x0, x0, #0, #48
  284: 0x9e620001  scvtf d1, x0
  288: 0x1e6118a4  fdiv d4, d5, d1
  292: 0xd2e7fe00  mov x0, #0x3ff0000000000000
  296: 0x9e670005  fmov d5, x0
  300: 0x1e653886  fsub d6, d4, d5
  304: 0x9e6600c0  fmov x0, d6
  308: 0xf9009740  str x0, [x26,#296]
  312: 0xd2800034  mov x20, #0x1
  316: 0xf9406f40  ldr x0, [x26,#216]
  320: 0x9340bc00  sbfx x0, x0, #0, #48
  324: 0xd1000415  sub x21, x0, #0x1
  328: 0x9340bea0  sbfx x0, x21, #0, #48
  332: 0xeb15001f  cmp x0, x21
  336: 0x540000e0  b.eq .+0x1c
  340: 0xd340be80  ubfx x0, x20, #0, #48
  344: 0xaa180000  orr x0, x0, x24
  348: 0xf9009b40  str x0, [x26,#304]
  352: 0xd2800040  mov x0, #0x2
  356: 0xf9000a60  str x0, [x19,#16]
  360: 0x140000cb  b .+0x32c
  364: 0xd340bea0  ubfx x0, x21, #0, #48
  368: 0xaa180000  orr x0, x0, x24
  372: 0xf9009f40  str x0, [x26,#312]
  376: 0xd2800034  mov x20, #0x1
  380: 0xd340be80  ubfx x0, x20, #0, #48
  384: 0xaa180000  orr x0, x0, x24
  388: 0xf900a340  str x0, [x26,#320]
  392: 0x92800016  mov x22, #0xffffffffffffffff
  396: 0xf9414340  ldr x0, [x26,#640]
  400: 0x9340bc14  sbfx x20, x0, #0, #48
  404: 0xd340be80  ubfx x0, x20, #0, #48
  408: 0xaa180000  orr x0, x0, x24
  412: 0xf9013340  str x0, [x26,#608]
  416: 0xaa1603f5  mov x21, x22
  420: 0x14000095  b .+0x254
  424: 0xd2e80000  mov x0, #0x4000000000000000
  428: 0x9e670004  fmov d4, x0
  432: 0xf9413b40  ldr x0, [x26,#624]
  436: 0x9340bc00  sbfx x0, x0, #0, #48
  440: 0x9e620001  scvtf d1, x0
  444: 0x1e610885  fmul d5, d4, d1
  448: 0xf9406f40  ldr x0, [x26,#216]
  452: 0x9340bc00  sbfx x0, x0, #0, #48
  456: 0x9e620001  scvtf d1, x0
  460: 0x1e6118a4  fdiv d4, d5, d1
  464: 0xd2e7ff00  mov x0, #0x3ff8000000000000
  468: 0x9e670005  fmov d5, x0
  472: 0x1e653886  fsub d6, d4, d5
  476: 0x9e6600c0  fmov x0, d6
  480: 0xf900bb40  str x0, [x26,#368]
  484: 0xd2800000  mov x0, #0x0
  488: 0x9e670004  fmov d4, x0
  492: 0x9e660080  fmov x0, d4
  496: 0xf900bf40  str x0, [x26,#376]
  500: 0xd2800000  mov x0, #0x0
  504: 0x9e670005  fmov d5, x0
  508: 0x9e6600a0  fmov x0, d5
  512: 0xf900c340  str x0, [x26,#384]
  516: 0xaa1903e0  mov x0, x25
  520: 0xaa0003f4  mov x20, x0
  524: 0xf900c754  str x20, [x26,#392]
  528: 0xd2800635  mov x21, #0x31
  532: 0xd340bea0  ubfx x0, x21, #0, #48
  536: 0xaa180000  orr x0, x0, x24
  540: 0xf900cb40  str x0, [x26,#400]
  544: 0xd2800036  mov x22, #0x1
  548: 0xd340bec0  ubfx x0, x22, #0, #48
  552: 0xaa180000  orr x0, x0, x24
  556: 0xf900cf40  str x0, [x26,#408]
  560: 0x92800017  mov x23, #0xffffffffffffffff
  564: 0x1e6040a0  fmov d0, d5
  568: 0x1e604085  fmov d5, d4
  572: 0x9e6600a0  fmov x0, d5
  576: 0xf9011340  str x0, [x26,#544]
  580: 0x1e604004  fmov d4, d0
  584: 0x9e660080  fmov x0, d4
  588: 0xf9010f40  str x0, [x26,#536]
  592: 0xaa1703f4  mov x20, x23
  596: 0x14000035  b .+0xd4
  600: 0xf9411340  ldr x0, [x26,#544]
  604: 0x9e670000  fmov d0, x0
  608: 0xf9411340  ldr x0, [x26,#544]
  612: 0x9e670001  fmov d1, x0
  616: 0x1e610804  fmul d4, d0, d1
  620: 0xf9410f40  ldr x0, [x26,#536]
  624: 0x9e670000  fmov d0, x0
  628: 0xf9410f40  ldr x0, [x26,#536]
  632: 0x9e670001  fmov d1, x0
  636: 0x1e610805  fmul d5, d0, d1
  640: 0x1e653886  fsub d6, d4, d5
  644: 0xf940bb40  ldr x0, [x26,#368]
  648: 0x9e670001  fmov d1, x0
  652: 0x1e6128c4  fadd d4, d6, d1
  656: 0x9e660080  fmov x0, d4
  660: 0xf900e340  str x0, [x26,#448]
  664: 0xd2e80000  mov x0, #0x4000000000000000
  668: 0x9e670005  fmov d5, x0
  672: 0xf9411340  ldr x0, [x26,#544]
  676: 0x9e670001  fmov d1, x0
  680: 0x1e6108a6  fmul d6, d5, d1
  684: 0xf9410f40  ldr x0, [x26,#536]
  688: 0x9e670001  fmov d1, x0
  692: 0x1e6108c5  fmul d5, d6, d1
  696: 0xf9409740  ldr x0, [x26,#296]
  700: 0x9e670001  fmov d1, x0
  704: 0x1e6128a6  fadd d6, d5, d1
  708: 0x9e6600c0  fmov x0, d6
  712: 0xf900f340  str x0, [x26,#480]
  716: 0xd2e80200  mov x0, #0x4010000000000000
  720: 0x9e670005  fmov d5, x0
  724: 0x1e640887  fmul d7, d4, d4
  728: 0x1e6608c8  fmul d8, d6, d6
  732: 0x1e6828e9  fadd d9, d7, d8
  736: 0x1e6920a0  fcmp d5, d9
  740: 0x9a9fa7e0  cset x0, lt
  744: 0xaa190000  orr x0, x0, x25
  748: 0xaa0003f4  mov x20, x0
  752: 0x37000134  tbnz w20, #0, .+0x24
  756: 0x1e604085  fmov d5, d4
  760: 0x9e6600a0  fmov x0, d5
  764: 0xf9011340  str x0, [x26,#544]
  768: 0x1e6040c4  fmov d4, d6
  772: 0x9e660080  fmov x0, d4
  776: 0xf9010f40  str x0, [x26,#536]
  780: 0xaa1503f4  mov x20, x21
  784: 0x14000006  b .+0x18
  788: 0x14000001  b .+0x4
  792: 0x91000720  add x0, x25, #0x1
  796: 0xaa0003f4  mov x20, x0
  800: 0xf9010b54  str x20, [x26,#528]
  804: 0x1400000d  b .+0x34
  808: 0x91000695  add x21, x20, #0x1
  812: 0xf940cb41  ldr x1, [x26,#400]
  816: 0x9340bc21  sbfx x1, x1, #0, #48
  820: 0xeb0102bf  cmp x21, x1
  824: 0x9a9fc7e0  cset x0, le
  828: 0xaa190000  orr x0, x0, x25
  832: 0xaa0003f4  mov x20, x0
  836: 0x37000094  tbnz w20, #0, .+0x10
  840: 0xf940c740  ldr x0, [x26,#392]
  844: 0xaa0003f4  mov x20, x0
  848: 0x14000002  b .+0x8
  852: 0x17ffffc1  b .+0xffffffffffffff04
  856: 0xaa1403e0  mov x0, x20
  860: 0xd2ffff81  mov x1, #0xfffc000000000000
  864: 0xeb01001f  cmp x0, x1
  868: 0x540000a0  b.eq .+0x14
  872: 0xeb19001f  cmp x0, x25
  876: 0x54000060  b.eq .+0xc
  880: 0x91000720  add x0, x25, #0x1
  884: 0x14000002  b .+0x8
  888: 0xaa1903e0  mov x0, x25
  892: 0xaa0003f5  mov x21, x0
  896: 0x37000055  tbnz w21, #0, .+0x8
  900: 0x14000009  b .+0x24
  904: 0xf9413340  ldr x0, [x26,#608]
  908: 0x9340bc14  sbfx x20, x0, #0, #48
  912: 0xd340be80  ubfx x0, x20, #0, #48
  916: 0xaa180000  orr x0, x0, x24
  920: 0xf9013340  str x0, [x26,#608]
  924: 0xf9413b40  ldr x0, [x26,#624]
  928: 0x9340bc15  sbfx x21, x0, #0, #48
  932: 0x14000015  b .+0x54
  936: 0xd2800034  mov x20, #0x1
  940: 0xf9413340  ldr x0, [x26,#608]
  944: 0x9340bc00  sbfx x0, x0, #0, #48
  948: 0x91000415  add x21, x0, #0x1
  952: 0x9340bea0  sbfx x0, x21, #0, #48
  956: 0xeb15001f  cmp x0, x21
  960: 0x540000e0  b.eq .+0x1c
  964: 0xd340be80  ubfx x0, x20, #0, #48
  968: 0xaa180000  orr x0, x0, x24
  972: 0xf9012b40  str x0, [x26,#592]
  976: 0xd2800040  mov x0, #0x2
  980: 0xf9000a60  str x0, [x19,#16]
  984: 0x1400002f  b .+0xbc
  988: 0xaa1503f4  mov x20, x21
  992: 0xd340be80  ubfx x0, x20, #0, #48
  996: 0xaa180000  orr x0, x0, x24
 1000: 0xf9013340  str x0, [x26,#608]
 1004: 0xf9413b40  ldr x0, [x26,#624]
 1008: 0x9340bc15  sbfx x21, x0, #0, #48
 1012: 0x14000001  b .+0x4
 1016: 0x910006b6  add x22, x21, #0x1
 1020: 0xd340bec0  ubfx x0, x22, #0, #48
 1024: 0xaa180000  orr x0, x0, x24
 1028: 0xf9013b40  str x0, [x26,#624]
 1032: 0xf9409f41  ldr x1, [x26,#312]
 1036: 0x9340bc21  sbfx x1, x1, #0, #48
 1040: 0xeb0102df  cmp x22, x1
 1044: 0x9a9fc7e0  cset x0, le
 1048: 0xaa190000  orr x0, x0, x25
 1052: 0xaa0003f5  mov x21, x0
 1056: 0x370000f5  tbnz w21, #0, .+0x1c
 1060: 0xd340be80  ubfx x0, x20, #0, #48
 1064: 0xaa180000  orr x0, x0, x24
 1068: 0xf9014340  str x0, [x26,#640]
 1072: 0xf9414b40  ldr x0, [x26,#656]
 1076: 0x9340bc15  sbfx x21, x0, #0, #48
 1080: 0x14000002  b .+0x8
 1084: 0x17ffff5b  b .+0xfffffffffffffd6c
 1088: 0x910006b6  add x22, x21, #0x1
 1092: 0xd340bec0  ubfx x0, x22, #0, #48
 1096: 0xaa180000  orr x0, x0, x24
 1100: 0xf9014b40  str x0, [x26,#656]
 1104: 0xf9407b41  ldr x1, [x26,#240]
 1108: 0x9340bc21  sbfx x1, x1, #0, #48
 1112: 0xeb0102df  cmp x22, x1
 1116: 0x9a9fc7e0  cset x0, le
 1120: 0xaa190000  orr x0, x0, x25
 1124: 0xaa0003f5  mov x21, x0
 1128: 0x37000055  tbnz w21, #0, .+0x8
 1132: 0x14000002  b .+0x8
 1136: 0x17ffff23  b .+0xfffffffffffffc8c
 1140: 0xf9414340  ldr x0, [x26,#640]
 1144: 0xf9000340  str x0, [x26]
 1148: 0xf9007e60  str x0, [x19,#248]
 1152: 0xf9409661  ldr x1, [x19,#296]
 1156: 0xb50003c1  cbnz x1, .+0x78
 1160: 0x14000001  b .+0x4
 1164: 0xd2800000  mov x0, #0x0
 1168: 0xf9000a60  str x0, [x19,#16]
 1172: 0x6d4627e8  ldp d8, d9, [sp,#96]
 1176: 0x6d472fea  ldp d10, d11, [sp,#112]
 1180: 0xa94573fb  ldp x27, x28, [sp,#80]
 1184: 0xa9446bf9  ldp x25, x26, [sp,#64]
 1188: 0xa94363f7  ldp x23, x24, [sp,#48]
 1192: 0xa9425bf5  ldp x21, x22, [sp,#32]
 1196: 0xa94153f3  ldp x19, x20, [sp,#16]
 1200: 0xa9407bfd  ldp x29, x30, [sp]
 1204: 0x910203ff  add sp, sp, #0x80
 1208: 0xd65f03c0  ret
 1212: 0xd10203ff  sub sp, sp, #0x80
 1216: 0xa9007bfd  stp x29, x30, [sp]
 1220: 0x910003fd  mov x29, sp
 1224: 0xa90153f3  stp x19, x20, [sp,#16]
 1228: 0xa9025bf5  stp x21, x22, [sp,#32]
 1232: 0xa90363f7  stp x23, x24, [sp,#48]
 1236: 0xa9046bf9  stp x25, x26, [sp,#64]
 1240: 0xa90573fb  stp x27, x28, [sp,#80]
 1244: 0x6d0627e8  stp d8, d9, [sp,#96]
 1248: 0x6d072fea  stp d10, d11, [sp,#112]
 1252: 0xaa0003f3  mov x19, x0
 1256: 0xf940027a  ldr x26, [x19]
 1260: 0xf940067b  ldr x27, [x19,#8]
 1264: 0xd2ffffd8  mov x24, #0xfffe000000000000
 1268: 0xd2ffffb9  mov x25, #0xfffd000000000000
 1272: 0x17fffed1  b .+0xfffffffffffffb44
 1276: 0xd2800000  mov x0, #0x0
 1280: 0xf9000a60  str x0, [x19,#16]
 1284: 0x6d4627e8  ldp d8, d9, [sp,#96]
 1288: 0x6d472fea  ldp d10, d11, [sp,#112]
 1292: 0xa94573fb  ldp x27, x28, [sp,#80]
 1296: 0xa9446bf9  ldp x25, x26, [sp,#64]
 1300: 0xa94363f7  ldp x23, x24, [sp,#48]
 1304: 0xa9425bf5  ldp x21, x22, [sp,#32]
 1308: 0xa94153f3  ldp x19, x20, [sp,#16]
 1312: 0xa9407bfd  ldp x29, x30, [sp]
 1316: 0x910203ff  add sp, sp, #0x80
 1320: 0xd65f03c0  ret
