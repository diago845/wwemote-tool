
Texture1D<float4> IniParams : register(t120);

#define VertexOffset IniParams[0].x
#define VertexCount  IniParams[0].y

#define EP_RECIPE_BASE 96
#define EP_RECIPE_SLOTS 64

Buffer<float> SparseData : register(t8);
RWBuffer<float> PositionDataRW : register(u6);

[numthreads(64,1,1)]
void main(uint3 vThreadID : SV_DispatchThreadID)
{
  if (vThreadID.x >= uint(VertexCount)) {
    return;
  }
  uint vertex_id = vThreadID.x + uint(VertexOffset);

  uint ent_base = uint(VertexCount) + 1;

  uint rs = asuint(SparseData.Load(vertex_id));
  uint re = asuint(SparseData.Load(vertex_id + 1));
  if (rs >= re) {
    return;
  }

  float slot_k[EP_RECIPE_SLOTS];
  float slot_w[EP_RECIPE_SLOTS];
  [unroll]
  for (int s = 0; s < EP_RECIPE_SLOTS; s++) {
    float4 r = IniParams[EP_RECIPE_BASE + s];
    slot_k[s] = r.x;
    slot_w[s] = r.y;
  }

  float3 acc = float3(0, 0, 0);
  for (uint i = rs; i < re; i++) {
    uint b = ent_base + i * 4;

    int key = (int)SparseData.Load(b + 0);
    float w = 0;
    [unroll]
    for (int s2 = 0; s2 < EP_RECIPE_SLOTS; s2++) {
      if ((int)slot_k[s2] == key) {
        w += slot_w[s2];
      }
    }
    if (w != 0) {
      acc += w * float3(SparseData.Load(b + 1),
                        SparseData.Load(b + 2),
                        SparseData.Load(b + 3));
    }
  }

  uint p = vertex_id * 3;
  PositionDataRW[p + 0] += acc.x;
  PositionDataRW[p + 1] += acc.y;
  PositionDataRW[p + 2] += acc.z;

  return;
}
