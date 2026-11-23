
Texture1D<float4> IniParams : register(t120);

RWBuffer<float4> CustomShapeKeyValuesRW : register(u5);

#define EP_U5_SHIELD_FLOATS 64

#define EP_U5_ZERO_ENCODE 1000000.0

#define EP_SHIELD_SLOT 160

[numthreads(64,1,1)]
void main(uint3 tid : SV_DispatchThreadID)
{
    uint i = tid.x;

    if (i >= EP_U5_SHIELD_FLOATS) { return; }

    float v = IniParams[EP_SHIELD_SLOT].x > 0.0 ? EP_U5_ZERO_ENCODE : 0.0;

    CustomShapeKeyValuesRW[i] = float4(v, v, v, v);
}
