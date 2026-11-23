
Texture1D<float4> IniParams : register(t120);

#define VertexOffset IniParams[0].x
#define VertexCount IniParams[0].y

#define EP_SHIELD_SLOT 160

Buffer<float3> PositionData : register(t6);
RWBuffer<float> ShapeKeyData : register(u0);
RWBuffer<float> PositionDataRW : register(u6);
RWBuffer<float4> DebugRW : register(u7);

[numthreads(64,1,1)]
void main(uint3 vThreadID : SV_DispatchThreadID)
{
  float4 positions, shape_key_vertex_offsets;

  if (vThreadID.x >= uint(VertexCount)) {
    return;
  }
  uint vertex_id = vThreadID.x + uint(VertexOffset);

  uint pos_id = vertex_id * 3;
  positions.xyz = PositionData.Load(vertex_id + 0).xyz;
  positions.w = 0;

  uint key_id = vertex_id * 6;
  shape_key_vertex_offsets.x = ShapeKeyData.Load(key_id + 0).x;
  shape_key_vertex_offsets.y = ShapeKeyData.Load(key_id + 1).x;
  shape_key_vertex_offsets.z = ShapeKeyData.Load(key_id + 2).x;
  shape_key_vertex_offsets.w = 0;

  float shield = IniParams[EP_SHIELD_SLOT].x;

  positions.xyz = positions.xyz + (shield > 0.0 ? float3(0.0, 0.0, 0.0) : shape_key_vertex_offsets.xyz);

  PositionDataRW[pos_id + 0].x = positions.x;
  PositionDataRW[pos_id + 1].x = positions.y;
  PositionDataRW[pos_id + 2].x = positions.z;

  return;
}
